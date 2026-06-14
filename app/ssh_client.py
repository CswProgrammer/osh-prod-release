from __future__ import annotations

import asyncio
import shlex
import subprocess


def ssh_cmd(
    host: str,
    port: str,
    user: str,
    password: str,
    remote: str,
    timeout: int = 120,
) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [
            "sshpass",
            "-p",
            password,
            "ssh",
            "-o",
            "StrictHostKeyChecking=no",
            "-o",
            "ConnectTimeout=30",
            "-p",
            port,
            f"{user}@{host}",
            remote,
        ],
        capture_output=True,
        text=True,
        timeout=timeout,
    )


def ssh_out(
    host: str,
    port: str,
    user: str,
    password: str,
    remote: str,
    timeout: int = 120,
) -> str:
    try:
        result = ssh_cmd(host, port, user, password, remote, timeout)
    except subprocess.TimeoutExpired as exc:
        raise RuntimeError(f"SSH 超时（{timeout}s）") from exc
    if result.returncode != 0:
        raise RuntimeError(result.stderr.strip() or result.stdout.strip() or f"ssh exit {result.returncode}")
    return result.stdout


def grep_remote_marker(
    host: str,
    port: str,
    user: str,
    password: str,
    log_glob: str,
    marker: str,
) -> bool:
    try:
        out = ssh_out(
            host,
            port,
            user,
            password,
            f"grep -l '{marker}' {log_glob} 2>/dev/null | head -1 || true",
            timeout=60,
        )
        return bool(out.strip())
    except RuntimeError:
        return False


def remote_line_count(
    host: str,
    port: str,
    user: str,
    password: str,
    log_path: str,
    timeout: int = 30,
) -> int:
    q = shlex.quote(log_path)
    out = ssh_out(host, port, user, password, f"test -f {q} && wc -l < {q} || echo 0", timeout=timeout)
    try:
        return int(out.strip())
    except ValueError:
        return 0


def grep_marker_after_line(
    host: str,
    port: str,
    user: str,
    password: str,
    log_path: str,
    marker: str,
    after_line: int,
    timeout: int = 60,
) -> bool:
    q = shlex.quote(log_path)
    safe_marker = marker.replace("'", "'\\''")
    start = max(after_line + 1, 1)
    cmd = f"tail -n +{start} {q} 2>/dev/null | grep -F '{safe_marker}' | head -1 || true"
    try:
        out = ssh_out(host, port, user, password, cmd, timeout=timeout)
        return bool(out.strip())
    except RuntimeError:
        return False


def tail_remote_new_lines(
    host: str,
    port: str,
    user: str,
    password: str,
    log_path: str,
    after_line: int,
    max_lines: int = 12,
    timeout: int = 45,
) -> tuple[list[str], int]:
    q = shlex.quote(log_path)
    start = max(after_line + 1, 1)
    cmd = (
        f"total=$(wc -l < {q} 2>/dev/null || echo 0); "
        f"tail -n +{start} {q} 2>/dev/null | tail -n {max_lines} | tr '\\r' '\\n' | tail -n {max_lines}; "
        f"printf '\\n__TOTAL_LINES__=%s\\n' \"$total\""
    )
    out = ssh_out(host, port, user, password, cmd, timeout=timeout)
    lines = out.splitlines()
    total = after_line
    if lines and lines[-1].startswith("__TOTAL_LINES__="):
        try:
            total = int(lines[-1].split("=", 1)[1])
        except ValueError:
            pass
        lines = lines[:-1]
    return lines, total


async def ssh_out_async(*args, **kwargs) -> str:
    return await asyncio.to_thread(ssh_out, *args, **kwargs)


async def grep_remote_marker_async(*args, **kwargs) -> bool:
    return await asyncio.to_thread(grep_remote_marker, *args, **kwargs)


async def remote_line_count_async(*args, **kwargs) -> int:
    return await asyncio.to_thread(remote_line_count, *args, **kwargs)


async def grep_marker_after_line_async(*args, **kwargs) -> bool:
    return await asyncio.to_thread(grep_marker_after_line, *args, **kwargs)


async def tail_remote_new_lines_async(*args, **kwargs) -> tuple[list[str], int]:
    return await asyncio.to_thread(tail_remote_new_lines, *args, **kwargs)
