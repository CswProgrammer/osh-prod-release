from __future__ import annotations

import asyncio
import json
import os
import re
import shlex
import time
import uuid
from datetime import datetime, timezone
from typing import Any

from app.config import DATA_DIR, STATE_FILE, cfg, mock_mode
from app.guards import GREEN_ENV_FILE, assert_green_only_config, remote_green_env_check_script
from app.models import Run, StepId, StepStatus, build_steps
from app.ssh_client import (
    grep_marker_after_line_async,
    grep_remote_marker_async,
    remote_line_count_async,
    ssh_out_async,
    tail_remote_new_lines_async,
)

RUNS: dict[str, Run] = {}
ACTIVE_RUN_ID: str | None = None
_lock = asyncio.Lock()


def _ensure_data_dir() -> None:
    DATA_DIR.mkdir(parents=True, exist_ok=True)


def save_state() -> None:
    _ensure_data_dir()
    payload = {rid: run.to_dict() for rid, run in RUNS.items()}
    STATE_FILE.write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")


def load_state() -> None:
    global RUNS
    if not STATE_FILE.exists():
        return
    try:
        data = json.loads(STATE_FILE.read_text(encoding="utf-8"))
    except json.JSONDecodeError:
        return
    for rid, raw in data.items():
        from app.models import Step

        steps_raw = raw.pop("steps", [])
        steps = [Step(**s) for s in steps_raw]
        RUNS[rid] = Run(steps=steps, **raw)
    _recover_stale_runs()


def _recover_stale_runs() -> None:
    """Server restart kills asyncio tasks; mark orphaned running jobs as failed."""
    changed = False
    for run in RUNS.values():
        if run.status != StepStatus.RUNNING:
            continue
        run.append_log("❌ 平台服务已重启，该任务已中断，请重新点击部署")
        run.status = StepStatus.FAILED
        for step in run.steps:
            if step.status == StepStatus.RUNNING:
                step.status = StepStatus.FAILED
                step.message = "服务重启中断"
                step.finished_at = datetime.now(timezone.utc).isoformat()
        changed = True
    if changed:
        save_state()


def step_by_id(run: Run, step_id: StepId):
    for s in run.steps:
        if s.id == step_id:
            return s
    raise KeyError(step_id)


def mark_step(run: Run, step_id: StepId, status: StepStatus, message: str = "") -> None:
    s = step_by_id(run, step_id)
    now = datetime.now(timezone.utc).isoformat()
    if status == StepStatus.RUNNING and not s.started_at:
        s.started_at = now
    if status in (StepStatus.SUCCESS, StepStatus.FAILED, StepStatus.SKIPPED):
        s.finished_at = now
    s.status = status
    if message:
        s.message = message
    save_state()


async def _stream_remote_log(
    run: Run,
    host: str,
    port: str,
    user: str,
    password: str,
    log_path: str,
    line_cursor: int,
    prefix: str,
    max_lines: int = 8,
) -> int:
    lines, total = await tail_remote_new_lines_async(
        host, port, user, password, log_path, line_cursor, max_lines=max_lines,
    )
    for line in lines:
        text = line.strip()
        if text and "__TOTAL_LINES__" not in text:
            run.append_log(f"{prefix} {text}")
    return total


async def wait_marker_in_log(
    run: Run,
    step_id: StepId,
    host: str,
    port: str,
    user: str,
    password: str,
    log_path: str,
    marker: str,
    line_offset: int,
    timeout_sec: int,
    poll_sec: int,
    log_prefix: str,
) -> None:
    mark_step(run, step_id, StepStatus.RUNNING)
    cursor = line_offset
    deadline = time.time() + timeout_sec
    while time.time() < deadline:
        cursor = await _stream_remote_log(
            run, host, port, user, password, log_path, cursor, log_prefix,
        )
        if await grep_marker_after_line_async(
            host, port, user, password, log_path, marker, line_offset,
        ):
            mark_step(run, step_id, StepStatus.SUCCESS, marker)
            run.append_log(f"✅ {marker}")
            save_state()
            return
        save_state()
        await asyncio.sleep(poll_sec)
    raise TimeoutError(f"超时未看到 {marker}（{timeout_sec}s，日志 {log_path}）")


async def _wait_backup_log_path(
    host: str,
    port: str,
    user: str,
    password: str,
    manual_log: str,
    log_dir: str,
    timeout_sec: int = 180,
) -> str:
    pattern = re.compile(r"日志:\s*(/\S+\.log)")
    deadline = time.time() + timeout_sec
    while time.time() < deadline:
        out = (
            await ssh_out_async(
                host, port, user, password,
                f"grep -F '日志:' {shlex.quote(manual_log)} 2>/dev/null | tail -1 || true",
                timeout=30,
            )
        ).strip()
        m = pattern.search(out)
        if m:
            return m.group(1)
        newest = (
            await ssh_out_async(
                host, port, user, password,
                f"ls -1t {shlex.quote(log_dir)}/osh-backup-*.log 2>/dev/null | head -1 || true",
                timeout=30,
            )
        ).strip()
        if newest:
            age = (
                await ssh_out_async(
                    host, port, user, password,
                    f"expr $(date +%s) - $(stat -c %Y {shlex.quote(newest)} 2>/dev/null || echo 0)",
                    timeout=20,
                )
            ).strip()
            try:
                if int(age) <= 120:
                    return newest
            except ValueError:
                pass
        await asyncio.sleep(3)
    raise TimeoutError(f"未找到本次 25 备份日志（wrapper: {manual_log}）")


async def _assert_remote_idle(
    host: str,
    port: str,
    user: str,
    password: str,
    script_path: str,
    label: str,
) -> None:
    q = shlex.quote(script_path)
    cmd = f"ps -eo args= | grep -F {q} | grep -v grep | head -1 || true"
    out = (await ssh_out_async(host, port, user, password, cmd, timeout=30)).strip()
    if out:
        raise RuntimeError(f"{label} 仍有任务在运行（{out[:120]}），请等待完成后再部署")


MOCK_LOGS: dict[StepId, list[str]] = {
    StepId.PRECHECK: ["SSH 43.242.200.25:58753 OK", "SSH 149.88.92.159:16328 OK", "磁盘 /www 剩余 42G"],
    StepId.BACKUP_TRIGGER: ["nohup osh-backup-25.sh started", "PID 28491"],
    StepId.BACKUP_PACK: ["rsync mirror 完成", "Nacos 导出 12 个 dataId", "SQL 快照 3 个脚本", "__OSH_BACKUP_PACK_DONE__"],
    StepId.BACKUP_UPLOAD: ["baidupcs upload osh-mirror.tar.gz", "进度 68%...", "__OSH_BACKUP_UPLOAD_DONE__"],
    StepId.BACKUP_DONE: ["上传成功.txt 已更新", "__OSH_BACKUP_ALL_DONE__"],
    StepId.PROD_PRE_BACKUP: ["mysqldump backstage → backup/pre-release/", "Nacos 配置导出 12 项", "__OSH_PROD_PRE_BACKUP_DONE__"],
    StepId.PROD_SQL: ["执行 alter_osh_course_section.sql", "执行 website_tag_add_use_count.sql", "__OSH_PROD_SQL_DONE__"],
    StepId.PROD_NACOS: ["发布 osh-backend-dev.yaml", "patch_runtime 改址 25→149", "__OSH_PROD_NACOS_DONE__"],
    StepId.PROD_CONFIG: ["nginx conf rsync", "nginx -t OK, reload", "__OSH_PROD_CONFIG_DONE__"],
    StepId.PROD_CODE: ["baidupcs download osh-mirror.tar.gz", "patch 25→149", "rsync → /opt/osh-green", "__OSH_PROD_CODE_ALL_DONE__"],
    StepId.PROD_RESTART: ["docker restart osh-g-backend", "已在 green-code-sync 内完成"],
    StepId.PROD_VERIFY: ["nginx=200 api=200 (28080)", "nacos=200 (28848)"],
    StepId.FINISHED: ["🎉 一键部署绿环境完成"],
}


async def _remote_executable(host: str, port: str, user: str, password: str, path: str) -> bool:
    out = await ssh_out_async(host, port, user, password, f"test -x {path} && echo yes || echo no", timeout=30)
    return "yes" in out.strip()


async def _run_prod_code_sync(
    run: Run,
    prod_host: str,
    prod_port: str,
    prod_user: str,
    prod_pass: str,
    poll: int,
    timeout_sec: int,
) -> None:
    sync_script = cfg("PROD_CODE_SYNC_SCRIPT", "/opt/osh-deploy-tools/osh-green-code-sync.sh")
    sync_log = cfg("PROD_CODE_SYNC_LOG", "/opt/osh-deploy-tools/logs/green-code-sync.log")
    all_marker = "__OSH_PROD_CODE_ALL_DONE__"
    download_marker = "__OSH_PROD_CODE_DOWNLOAD_DONE__"
    apply_marker = "__OSH_PROD_CODE_APPLY_DONE__"

    mark_step(run, StepId.PROD_CODE, StepStatus.RUNNING)
    await _assert_remote_idle(prod_host, prod_port, prod_user, prod_pass, sync_script, "149 绿环境 code-sync")

    line_before = await remote_line_count_async(prod_host, prod_port, prod_user, prod_pass, sync_log)
    run.append_log(f"149 部署日志: {sync_log}（从第 {line_before + 1} 行起监控本次任务）")

    sep_cmd = (
        f"mkdir -p {shlex.quote(os.path.dirname(sync_log))} && "
        f"echo '=== deploy run {run.id} ===' >> {shlex.quote(sync_log)}"
    )
    await ssh_out_async(prod_host, prod_port, prod_user, prod_pass, sep_cmd, timeout=30)
    line_offset = await remote_line_count_async(prod_host, prod_port, prod_user, prod_pass, sync_log)

    prod_cmd = (
        f"mkdir -p {shlex.quote(os.path.dirname(sync_log))} && "
        f"( setsid bash {shlex.quote(sync_script)} all >> {shlex.quote(sync_log)} 2>&1 < /dev/null & ); "
        f"sleep 1; pgrep -f {shlex.quote(sync_script)} | tail -1 || echo started"
    )
    run.append_log(f"149 启动: bash {sync_script} all")
    pid = (await ssh_out_async(prod_host, prod_port, prod_user, prod_pass, prod_cmd, timeout=180)).strip()
    run.append_log(f"149 绿环境 code-sync 已启动 PID={pid}")
    save_state()

    saw_download = False
    saw_apply = False
    cursor = line_offset
    deadline = time.time() + timeout_sec
    while time.time() < deadline:
        cursor = await _stream_remote_log(
            run, prod_host, prod_port, prod_user, prod_pass, sync_log, cursor, "[149 部署]",
        )
        if not saw_download and await grep_marker_after_line_async(
            prod_host, prod_port, prod_user, prod_pass, sync_log, download_marker, line_offset,
        ):
            saw_download = True
            run.append_log(f"✅ {download_marker}（网盘下载/解压完成）")
            save_state()
        if not saw_apply and await grep_marker_after_line_async(
            prod_host, prod_port, prod_user, prod_pass, sync_log, apply_marker, line_offset,
        ):
            saw_apply = True
            run.append_log(f"✅ {apply_marker}（已覆盖绿环境代码并重启）")
            save_state()
        if await grep_marker_after_line_async(
            prod_host, prod_port, prod_user, prod_pass, sync_log, all_marker, line_offset,
        ):
            mark_step(run, StepId.PROD_CODE, StepStatus.SUCCESS, all_marker)
            mark_step(run, StepId.PROD_RESTART, StepStatus.SUCCESS, "code-sync 含重启")
            run.append_log(f"✅ {all_marker}")
            green_stat = (
                await ssh_out_async(
                    prod_host, prod_port, prod_user, prod_pass,
                    "stat -c 'jar %y' /opt/osh-green/app/osh/osh-backend/backstage-admin.jar 2>/dev/null; "
                    "stat -c 'frontend %y' /opt/osh-green/app/osh/osh-frontend/html/index.html 2>/dev/null || true",
                    timeout=30,
                )
            ).strip()
            if green_stat:
                run.append_log(f"149 绿环境文件更新时间: {green_stat.replace(chr(10), ' | ')}")
            save_state()
            return
        save_state()
        await asyncio.sleep(poll)
    raise TimeoutError(f"code-sync 超时（{timeout_sec}s）")


async def execute_mock_pipeline(run: Run) -> None:
    global ACTIVE_RUN_ID
    delay = float(cfg("MOCK_STEP_DELAY_SEC", "1.2"))
    try:
        for step in run.steps:
            if step.status == StepStatus.SKIPPED:
                continue
            mark_step(run, step.id, StepStatus.RUNNING)
            for line in MOCK_LOGS.get(step.id, [f"mock {step.id}"]):
                run.append_log(line)
                save_state()
                await asyncio.sleep(delay * 0.4)
            mark_step(run, step.id, StepStatus.SUCCESS)
            save_state()
            await asyncio.sleep(delay * 0.3)
        mark_step(run, StepId.FINISHED, StepStatus.SUCCESS, "演示模式完成")
        run.status = StepStatus.SUCCESS
    except Exception as exc:
        run.append_log(f"❌ {exc}")
        run.status = StepStatus.FAILED
    finally:
        save_state()
        ACTIVE_RUN_ID = None


async def execute_real_pipeline(run: Run) -> None:
    global ACTIVE_RUN_ID
    test_host = cfg("TEST_HOST")
    test_port = cfg("TEST_PORT", "58753")
    test_user = cfg("TEST_USER", "root")
    test_pass = cfg("TEST_PASSWORD")
    prod_host = cfg("PROD_HOST")
    prod_port = cfg("PROD_PORT", "16328")
    prod_user = cfg("PROD_USER", "root")
    prod_pass = cfg("PROD_PASSWORD")
    poll = int(cfg("POLL_INTERVAL_SEC", "10"))
    backup_timeout = int(cfg("BACKUP_TIMEOUT_SEC", "7200"))
    release_timeout = int(cfg("PROD_RELEASE_TIMEOUT_SEC", "3600"))

    skip_backup = run.mode in ("skip_backup", "code_only")
    use_release = cfg("PROD_USE_RELEASE", "false").lower() == "true"

    backup_script = cfg("TEST_BACKUP_SCRIPT", "/www/osh-backup-tools/osh-backup-25.sh")
    sync_script = cfg("PROD_CODE_SYNC_SCRIPT", "/opt/osh-deploy-tools/osh-green-code-sync.sh")
    release_script = cfg("PROD_RELEASE_SCRIPT", "/opt/osh-deploy-tools/osh-prod-release.sh")

    try:
        assert_green_only_config()
        mark_step(run, StepId.PRECHECK, StepStatus.RUNNING)
        run.append_log("🔒 蓝项目保护：本地配置已锁定为仅部署绿环境")
        for label, h, p, u, pw in [
            ("测试机", test_host, test_port, test_user, test_pass),
            ("生产机", prod_host, prod_port, prod_user, prod_pass),
        ]:
            await ssh_out_async(h, p, u, pw, "echo ok", timeout=30)
            run.append_log(f"SSH {label} {h}:{p} OK")
        if not await _remote_executable(test_host, test_port, test_user, test_pass, backup_script):
            raise RuntimeError(f"25 缺少可执行备份脚本: {backup_script}")
        run.append_log(f"25 备份脚本 OK: {backup_script}")
        if use_release:
            if not await _remote_executable(prod_host, prod_port, prod_user, prod_pass, release_script):
                raise RuntimeError(f"已启用 PROD_USE_RELEASE 但 149 无脚本: {release_script}")
            run.append_log(f"149 发版脚本 OK: {release_script}")
        else:
            if not await _remote_executable(prod_host, prod_port, prod_user, prod_pass, sync_script):
                raise RuntimeError(f"149 缺少可执行同步脚本: {sync_script}")
            run.append_log(f"149 绿环境同步脚本 OK: {sync_script}")
            env_out = (
                await ssh_out_async(
                    prod_host, prod_port, prod_user, prod_pass,
                    remote_green_env_check_script(GREEN_ENV_FILE),
                    timeout=30,
                )
            ).strip()
            if "green_env_ok" not in env_out:
                raise RuntimeError(f"149 绿环境配置未通过蓝项目保护校验: {env_out}")
            run.append_log(f"149 {GREEN_ENV_FILE} 校验 OK（/opt/osh-green + osh-g-*）")
            blue_http = (
                await ssh_out_async(
                    prod_host, prod_port, prod_user, prod_pass,
                    "curl -s -o /dev/null -w '%{http_code}' --max-time 10 http://127.0.0.1:58080/ || echo 000",
                    timeout=20,
                )
            ).strip()
            run.append_log(f"蓝项目基线 :58080 HTTP {blue_http}（只读探测，不修改）")
        if not skip_backup:
            lock = cfg("TEST_BACKUP_LOCK", "/www/osh-backup-tools/.backup.lock")
            locked = await ssh_out_async(test_host, test_port, test_user, test_pass, f"test -f {lock} && echo locked || echo free")
            if "locked" in locked:
                run.append_log("⚠️ 备份锁存在，将检查是否有进程在跑")
            await _assert_remote_idle(
                test_host, test_port, test_user, test_pass,
                backup_script, "25 备份",
            )
        mark_step(run, StepId.PRECHECK, StepStatus.SUCCESS)
        save_state()

        if not skip_backup:
            script = backup_script
            log_dir = cfg("TEST_BACKUP_LOG_DIR", "/www/osh-backup-tools/logs")
            manual_log = f"{log_dir}/deploy-{run.id}.log"
            mark_step(run, StepId.BACKUP_TRIGGER, StepStatus.RUNNING)
            trigger = (
                f"mkdir -p {shlex.quote(log_dir)} && "
                f"( setsid bash {shlex.quote(script)} > {shlex.quote(manual_log)} 2>&1 < /dev/null & ); "
                f"sleep 1; pgrep -f {shlex.quote(script)} | tail -1 || echo started"
            )
            pid = (await ssh_out_async(test_host, test_port, test_user, test_pass, trigger, timeout=180)).strip()
            run.append_log(f"25 已启动备份 PID={pid}，wrapper 日志: {manual_log}")
            backup_log = await _wait_backup_log_path(
                test_host, test_port, test_user, test_pass, manual_log, log_dir,
            )
            run.append_log(f"25 备份主日志: {backup_log}")
            mark_step(run, StepId.BACKUP_TRIGGER, StepStatus.SUCCESS, f"PID={pid}")
            save_state()

            for sid, marker in [
                (StepId.BACKUP_PACK, "__OSH_BACKUP_PACK_DONE__"),
                (StepId.BACKUP_UPLOAD, "__OSH_BACKUP_UPLOAD_DONE__"),
                (StepId.BACKUP_DONE, "__OSH_BACKUP_ALL_DONE__"),
            ]:
                await wait_marker_in_log(
                    run, sid, test_host, test_port, test_user, test_pass,
                    backup_log, marker, line_offset=0,
                    timeout_sec=backup_timeout, poll_sec=poll, log_prefix="[25 备份]",
                )

            state_ok = cfg("TEST_BACKUP_STATE_OK", "/www/osh-backup-tools/.backup-last-ok")
            upload_marker = cfg("TEST_BACKUP_UPLOAD_MARKER", "/www/osh-backup-tools/上传成功.txt")
            ok_ts = (
                await ssh_out_async(
                    test_host, test_port, test_user, test_pass,
                    f"echo -n 'state_ok='; cat {shlex.quote(state_ok)} 2>/dev/null || echo missing; "
                    f"echo; echo -n 'upload_marker='; head -3 {shlex.quote(upload_marker)} 2>/dev/null | tr '\\n' ' ' || echo missing",
                    timeout=30,
                )
            ).strip().replace("\n", " | ")
            run.append_log(f"25 网盘上传确认: {ok_ts}")

        if use_release:
            release_log = cfg("PROD_RELEASE_LOG", "/opt/osh-deploy-tools/logs/prod-release.log")
            mark_step(run, StepId.PROD_PRE_BACKUP, StepStatus.RUNNING)
            prod_cmd = (
                f"mkdir -p $(dirname {shlex.quote(release_log)}) && "
                f"nohup bash {shlex.quote(release_script)} all >> {shlex.quote(release_log)} 2>&1 & echo $!"
            )
            pid = (await ssh_out_async(prod_host, prod_port, prod_user, prod_pass, prod_cmd, timeout=90)).strip()
            run.append_log(f"prod-release PID={pid}")
            save_state()

            step_markers = [
                (StepId.PROD_PRE_BACKUP, "__OSH_PROD_PRE_BACKUP_DONE__"),
                (StepId.PROD_SQL, "__OSH_PROD_SQL_DONE__"),
                (StepId.PROD_NACOS, "__OSH_PROD_NACOS_DONE__"),
                (StepId.PROD_CONFIG, "__OSH_PROD_CONFIG_DONE__"),
                (StepId.PROD_CODE, "__OSH_PROD_CODE_DONE__"),
                (StepId.PROD_RESTART, "__OSH_PROD_RESTART_DONE__"),
            ]
            seen: set[StepId] = set()
            deadline = time.time() + release_timeout
            while time.time() < deadline:
                if await grep_remote_marker_async(prod_host, prod_port, prod_user, prod_pass, release_log, "__OSH_PROD_RELEASE_DONE__"):
                    for sid, _ in step_markers:
                        if step_by_id(run, sid).status not in (StepStatus.SKIPPED, StepStatus.SUCCESS):
                            mark_step(run, sid, StepStatus.SUCCESS)
                    break
                for sid, marker in step_markers:
                    if sid in seen or step_by_id(run, sid).status == StepStatus.SKIPPED:
                        continue
                    if await grep_remote_marker_async(prod_host, prod_port, prod_user, prod_pass, release_log, marker):
                        mark_step(run, sid, StepStatus.SUCCESS, marker)
                        run.append_log(f"✅ {marker}")
                        seen.add(sid)
                        save_state()
                    elif step_by_id(run, sid).status == StepStatus.PENDING and (
                        not seen or sid == step_markers[len(seen)][0]
                    ):
                        mark_step(run, sid, StepStatus.RUNNING)
                tail = await ssh_out_async(prod_host, prod_port, prod_user, prod_pass, f"tail -2 {release_log} 2>/dev/null || true", timeout=30)
                run.append_log(tail.strip()[-180:])
                save_state()
                await asyncio.sleep(poll)
            else:
                raise TimeoutError("prod-release 超时")
        else:
            await _run_prod_code_sync(
                run, prod_host, prod_port, prod_user, prod_pass, poll, release_timeout,
            )

        mark_step(run, StepId.PROD_VERIFY, StepStatus.RUNNING)
        nginx_p = cfg("PROD_NGINX_PORT", "28080")
        backend_p = cfg("PROD_BACKEND_PORT", "28081")
        nacos_p = cfg("PROD_NACOS_PORT", "28848")
        verify_script = (
            f"c1=$(curl -s -o /dev/null -w '%{{http_code}}' --max-time 15 http://127.0.0.1:{nginx_p}/ || echo 000); "
            f"c2=$(curl -s -o /dev/null -w '%{{http_code}}' --max-time 15 -X POST "
            f"http://127.0.0.1:{nginx_p}/pc/course/search -H 'Content-Type: application/json' "
            f"-d '{{\"pageNum\":1,\"pageSize\":1}}' || echo 000); "
            f"c3=$(curl -s -o /dev/null -w '%{{http_code}}' --max-time 15 http://127.0.0.1:{nacos_p}/nacos/ || echo 000); "
            f"echo nginx=$c1 api=$c2 nacos=$c3"
        )
        vout = await ssh_out_async(prod_host, prod_port, prod_user, prod_pass, verify_script, timeout=60)
        run.append_log(vout.strip())
        m = re.search(r"nginx=(\d+).*api=(\d+).*nacos=(\d+)", vout)
        if not m:
            raise RuntimeError(f"验收输出异常: {vout}")
        c1, c2, c3 = m.group(1), m.group(2), m.group(3)
        if c1 not in ("200", "301", "302") or c2 not in ("200", "401") or c3 not in ("200", "302"):
            raise RuntimeError(f"绿环境验收失败 nginx={c1} api={c2} nacos={c3}")
        mark_step(run, StepId.PROD_VERIFY, StepStatus.SUCCESS, vout.strip())
        blue_after = (
            await ssh_out_async(
                prod_host, prod_port, prod_user, prod_pass,
                "curl -s -o /dev/null -w '%{http_code}' --max-time 10 http://127.0.0.1:58080/ || echo 000",
                timeout=20,
            )
        ).strip()
        run.append_log(f"蓝项目部署后 :58080 HTTP {blue_after}（应仍正常，未触碰蓝栈）")

        mark_step(run, StepId.FINISHED, StepStatus.SUCCESS, "一键部署绿环境完成")
        run.status = StepStatus.SUCCESS
    except Exception as exc:
        run.append_log(f"❌ {exc}")
        for s in run.steps:
            if s.status == StepStatus.RUNNING:
                mark_step(run, s.id, StepStatus.FAILED, str(exc))
                break
        run.status = StepStatus.FAILED
    finally:
        save_state()
        ACTIVE_RUN_ID = None


async def execute_pipeline(run: Run) -> None:
    if mock_mode():
        run.append_log("⚙️ 演示模式（MOCK_MODE=true 或未配置 SSH 密码）")
        save_state()
        await execute_mock_pipeline(run)
    else:
        await execute_real_pipeline(run)


async def start_run(mode: str) -> str:
    global ACTIVE_RUN_ID
    if mode not in ("standard", "skip_backup", "code_only"):
        raise ValueError("invalid mode")

    async with _lock:
        for existing in RUNS.values():
            if existing.status == StepStatus.RUNNING:
                raise RuntimeError("已有任务在运行，请等待完成或刷新页面")

        if ACTIVE_RUN_ID:
            active = RUNS.get(ACTIVE_RUN_ID)
            if active and active.status == StepStatus.RUNNING:
                raise RuntimeError("已有任务在运行")

        assert_green_only_config()

        run_id = uuid.uuid4().hex[:12]
        use_release = cfg("PROD_USE_RELEASE", "false").lower() == "true"
        run = Run(
            id=run_id,
            mode=mode,
            status=StepStatus.RUNNING,
            created_at=datetime.now(timezone.utc).isoformat(),
            steps=build_steps(mode, use_release=use_release),
        )
        RUNS[run_id] = run
        ACTIVE_RUN_ID = run_id
        run.append_log(f"🚀 任务已启动 mode={mode}")
        if mode == "standard":
            run.append_log("📦 完整流程：25 打包备份 → 上传网盘 → 149 下载 → 绿环境部署")
        elif mode == "skip_backup":
            run.append_log("⏭️ 已跳过 25 备份/网盘上传（直接用网盘现有包）。要更新网盘请点「一键部署绿环境」")
            for s in run.steps:
                if s.id.value.startswith("backup_"):
                    s.message = "本任务选择了跳过备份"
        elif mode == "code_only":
            run.append_log("⏭️ 仅更代码：跳过 25 备份，149 只同步代码")
            for s in run.steps:
                if s.id.value.startswith("backup_"):
                    s.message = "本任务选择了跳过备份"
        save_state()
        asyncio.create_task(execute_pipeline(run))
        return run_id


def list_runs() -> list[dict[str, Any]]:
    return [
        {"id": r.id, "mode": r.mode, "status": r.status, "created_at": r.created_at}
        for r in sorted(RUNS.values(), key=lambda x: x.created_at, reverse=True)
    ]


def get_run(run_id: str) -> Run:
    if run_id not in RUNS:
        raise KeyError(run_id)
    return RUNS[run_id]


load_state()
