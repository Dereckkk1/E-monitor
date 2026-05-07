import base64
import contextlib

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field

from model import embed, load_model


@contextlib.asynccontextmanager
async def lifespan(app: FastAPI):
    load_model()
    yield


app = FastAPI(lifespan=lifespan)


class EmbedRequest(BaseModel):
    pcm_b64: str
    sr: int = Field(default=16000, ge=8000, le=48000)


class EmbedResponse(BaseModel):
    embedding: list[float]


@app.post("/embed", response_model=EmbedResponse)
async def embed_endpoint(req: EmbedRequest) -> EmbedResponse:
    try:
        pcm_bytes = base64.b64decode(req.pcm_b64)
    except Exception:
        raise HTTPException(status_code=422, detail="Invalid base64 in pcm_b64")
    try:
        vec = embed(pcm_bytes, req.sr)
        return EmbedResponse(embedding=vec)
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))


@app.get("/health")
async def health() -> dict:
    from model import MOCK_MODE, _session
    model_ready = MOCK_MODE or _session is not None
    return {"status": "ok" if model_ready else "loading", "model_ready": model_ready}
