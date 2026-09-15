from contextlib import asynccontextmanager
from typing import Literal
from uuid import uuid4

import torch
from fastapi import FastAPI, HTTPException
from FlagEmbedding import BGEM3FlagModel
from pydantic import BaseModel, Field


MODEL_NAME = "BAAI/bge-m3"
model: BGEM3FlagModel | None = None


@asynccontextmanager
async def lifespan(_: FastAPI):
    global model
    model = BGEM3FlagModel(MODEL_NAME, use_fp16=torch.cuda.is_available())
    yield
    model = None


app = FastAPI(title="Narra BGE-M3 Embedding Service", lifespan=lifespan)


class EmbeddingRequest(BaseModel):
    input: str | list[str] = Field(min_length=1)
    model: str = MODEL_NAME
    encoding_format: Literal["float"] = "float"


@app.get("/health")
def health() -> dict[str, str]:
    if model is None:
        raise HTTPException(status_code=503, detail="model is loading")
    return {"status": "ok", "model": MODEL_NAME}


@app.post("/v1/embeddings")
def embeddings(request: EmbeddingRequest) -> dict[str, object]:
    if model is None:
        raise HTTPException(status_code=503, detail="model is loading")
    if request.model != MODEL_NAME:
        raise HTTPException(status_code=400, detail=f"only {MODEL_NAME} is available")

    inputs = [request.input] if isinstance(request.input, str) else request.input
    if not inputs or any(not text.strip() for text in inputs):
        raise HTTPException(status_code=400, detail="input must contain non-empty text")

    dense_vectors = model.encode(
        inputs,
        return_dense=True,
        return_sparse=False,
        return_colbert_vecs=False,
    )["dense_vecs"]
    data = [
        {"object": "embedding", "index": index, "embedding": vector.tolist()}
        for index, vector in enumerate(dense_vectors)
    ]
    return {
        "object": "list",
        "data": data,
        "model": MODEL_NAME,
        "usage": {"prompt_tokens": 0, "total_tokens": 0},
        "id": f"embd-{uuid4().hex}",
    }
