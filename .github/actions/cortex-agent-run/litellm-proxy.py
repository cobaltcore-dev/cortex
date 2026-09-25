"""
Minimal Anthropic Messages API proxy using LiteLLM SDK.

Accepts Anthropic Messages API requests on /v1/messages and routes them
through LiteLLM to SAP AI Core (or any other LiteLLM-supported provider).

Uses litellm.anthropic.messages.acreate() which handles Anthropic format
natively — no format translation, no Pydantic serialization bugs.
"""

import json
import logging
import os
import sys

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse, StreamingResponse

import litellm

# Drop unsupported params (e.g., 'thinking') instead of raising errors.
# Claude Code CLI sends params that not all providers support.
litellm.drop_params = True

logging.basicConfig(level=logging.INFO, stream=sys.stderr)
logger = logging.getLogger("litellm-proxy")

app = FastAPI()

# The LiteLLM model to route requests to (e.g., "sap/anthropic--claude-4.6-opus")
LITELLM_MODEL = os.environ.get("LITELLM_MODEL", "sap/anthropic--claude-4.6-opus")


@app.get("/health/readiness")
async def health_readiness():
    return {"status": "ok"}


@app.post("/v1/messages")
async def messages(request: Request):
    try:
        body = await request.json()
    except Exception:
        return JSONResponse(
            status_code=400,
            content={
                "type": "error",
                "error": {
                    "type": "invalid_request_error",
                    "message": "Request body is not valid JSON.",
                },
            },
        )

    original_model = body.get("model", "unknown")
    body["model"] = LITELLM_MODEL

    logger.info(
        f"Proxying request: {original_model} -> {LITELLM_MODEL}, stream={body.get('stream', False)}"
    )

    is_streaming = body.get("stream", False)

    try:
        if is_streaming:
            return await _handle_streaming(body)
        else:
            return await _handle_non_streaming(body)
    except Exception as e:
        logger.exception(f"Error proxying request: {e}")
        return JSONResponse(
            status_code=500,
            content={
                "type": "error",
                "error": {
                    "type": "api_error",
                    "message": "An internal proxy error occurred. Check server logs for details.",
                },
            },
        )


async def _handle_non_streaming(body: dict) -> JSONResponse:
    body.pop("stream", None)

    response = await litellm.anthropic.messages.acreate(**body)

    if hasattr(response, "model_dump"):
        result = response.model_dump()
    elif isinstance(response, dict):
        result = response
    else:
        result = json.loads(str(response))

    return JSONResponse(content=result)


async def _handle_streaming(body: dict) -> StreamingResponse:
    body["stream"] = True

    response = await litellm.anthropic.messages.acreate(**body)

    async def event_generator():
        try:
            async for chunk in response:
                if hasattr(chunk, "model_dump"):
                    data = chunk.model_dump()
                elif isinstance(chunk, dict):
                    data = chunk
                else:
                    data = json.loads(str(chunk))

                event_type = data.get("type")
                if not event_type:
                    logger.warning("Chunk missing 'type' field: %s", data)
                    continue
                yield f"event: {event_type}\ndata: {json.dumps(data)}\n\n"
        except Exception as e:
            logger.exception(f"Streaming error: {e}")
            error_data = {
                "type": "error",
                "error": {"type": "api_error", "message": "An internal proxy error occurred. Check server logs for details."},
            }
            yield f"event: error\ndata: {json.dumps(error_data)}\n\n"

    return StreamingResponse(
        event_generator(),
        media_type="text/event-stream",
        headers={
            "Cache-Control": "no-cache",
            "Connection": "keep-alive",
            "X-Accel-Buffering": "no",
        },
    )


if __name__ == "__main__":
    import uvicorn

    port = int(os.environ.get("LITELLM_PROXY_PORT", "4000"))
    logger.info(f"Starting LiteLLM proxy on port {port}, model={LITELLM_MODEL}")
    uvicorn.run(app, host="127.0.0.1", port=port, log_level="info")
