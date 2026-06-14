from __future__ import annotations

import json
from pathlib import Path

from fastapi import FastAPI, Header, HTTPException
from fastapi.responses import FileResponse, StreamingResponse
from fastapi.staticfiles import StaticFiles
from pydantic import BaseModel

from app.config import STATIC_DIR, cfg, mock_mode
from app.guards import assert_green_only_config
from app.models import StepStatus
from app.orchestrator import get_run, list_runs, start_run

app = FastAPI(title="OSH Prod Release", version="1.0.0")

if STATIC_DIR.is_dir():
    assets = STATIC_DIR / "assets"
    if assets.is_dir():
        app.mount("/assets", StaticFiles(directory=assets), name="assets")


class StartRequest(BaseModel):
    mode: str = "standard"


def auth(token: str | None) -> None:
    expected = cfg("API_TOKEN")
    if expected and token != expected:
        raise HTTPException(401, "invalid token")


@app.get("/api/health")
async def health() -> dict:
    use_release = cfg("PROD_USE_RELEASE", "false").lower() == "true"
    deploy_target = cfg("PROD_DEPLOY_TARGET", "green")
    nginx_p = cfg("PROD_NGINX_PORT", "28080")
    blue_protection = {"ok": True, "note": "仅绿环境；蓝 /opt/osh 不可写"}
    try:
        assert_green_only_config()
    except RuntimeError as exc:
        blue_protection = {"ok": False, "error": str(exc)}
    return {
        "status": "ok" if blue_protection["ok"] else "misconfigured",
        "mock_mode": mock_mode(),
        "test_host": cfg("TEST_HOST"),
        "prod_host": cfg("PROD_HOST"),
        "deploy_path": "prod-release" if use_release else "green-code-sync",
        "deploy_target": deploy_target,
        "green_url": f"http://{cfg('PROD_HOST')}:{nginx_p}/",
        "blue_protection": blue_protection,
        "prod_use_release": use_release,
    }


@app.get("/api/runs")
async def api_list_runs():
    return list_runs()


@app.get("/api/runs/{run_id}")
async def api_get_run(run_id: str):
    try:
        return get_run(run_id).to_dict()
    except KeyError:
        raise HTTPException(404, "run not found")


@app.post("/api/deploy/start")
async def api_start(body: StartRequest, authorization: str | None = Header(None)):
    auth(authorization.replace("Bearer ", "") if authorization else None)
    try:
        run_id = await start_run(body.mode)
    except ValueError:
        raise HTTPException(400, "mode must be standard | skip_backup | code_only")
    except RuntimeError as exc:
        raise HTTPException(409, str(exc))
    return {"run_id": run_id}


@app.get("/api/runs/{run_id}/stream")
async def api_stream(run_id: str):
    try:
        get_run(run_id)
    except KeyError:
        raise HTTPException(404, "run not found")

    async def gen():
        while True:
            try:
                run = get_run(run_id)
            except KeyError:
                break
            yield f"data: {json.dumps(run.to_dict(), ensure_ascii=False)}\n\n"
            if run.status in (StepStatus.SUCCESS, StepStatus.FAILED):
                break
            import asyncio

            await asyncio.sleep(1.5)

    return StreamingResponse(
        gen(),
        media_type="text/event-stream",
        headers={
            "Cache-Control": "no-cache",
            "Connection": "keep-alive",
            "X-Accel-Buffering": "no",
        },
    )


@app.get("/")
async def index():
    index_file = STATIC_DIR / "index.html"
    if index_file.is_file():
        return FileResponse(index_file)
    return {"message": "前端未构建，请运行: cd web && npm ci && npm run build"}


@app.get("/{full_path:path}")
async def spa_fallback(full_path: str):
    if full_path.startswith("api/"):
        raise HTTPException(404)
    index_file = STATIC_DIR / "index.html"
    if index_file.is_file():
        return FileResponse(index_file)
    raise HTTPException(404)


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(
        "app.main:app",
        host=cfg("BIND_HOST", "127.0.0.1"),
        port=int(cfg("BIND_PORT", "8765")),
        reload=False,
    )
