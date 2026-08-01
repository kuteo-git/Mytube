"""
NLLB-200 translation server — EN -> VI for YouTube captions.
Default port 8003.  Accepts POST JSON {"text": "…", "src": "eng_Latn", "tgt": "vie_Latn"}.

Model: facebook/nllb-200-distilled-600M (~1.2 GB, runs on M4 Metal via MPS).
First call is slow (~30s to load + warm up); subsequent calls ~0.3-0.5s per sentence.
"""
import os
import time
import torch
import uvicorn
from fastapi import FastAPI, Request
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse
from transformers import AutoTokenizer, AutoModelForSeq2SeqLM, MarianMTModel

MODEL_NAME = os.environ.get("NLLB_MODEL", "Helsinki-NLP/opus-mt-en-vi")
DEVICE = "mps" if torch.backends.mps.is_available() else "cpu"
PORT = int(os.environ.get("NLLB_PORT", "8005"))

print(f"Loading NLLB-200 on {DEVICE}...")
tokenizer = AutoTokenizer.from_pretrained(MODEL_NAME)
model = AutoModelForSeq2SeqLM.from_pretrained(MODEL_NAME).to(DEVICE)
print(f"Ready on port {PORT}")

app = FastAPI()
app.add_middleware(CORSMiddleware, allow_origins=["*"], allow_methods=["*"], allow_headers=["*"])

@app.get("/health")
def health():
    return {"status": "ok", "model": MODEL_NAME, "device": DEVICE}

@app.post("/translate")
async def translate(req: Request):
    body = await req.json()
    text = (body.get("text") or "").strip()
    if not text:
        return JSONResponse({"translated": ""})

    src_lang = body.get("src", "eng_Latn")
    tgt_lang = body.get("tgt", "vie_Latn")

    inputs = tokenizer(text, return_tensors="pt").to(DEVICE)

    t0 = time.perf_counter()
    with torch.no_grad():
        outputs = model.generate(
            **inputs,
            max_new_tokens=min(256, max(32, int(len(text.split()) * 3))),
            num_beams=4,
        )
    translated = tokenizer.batch_decode(outputs, skip_special_tokens=True)[0]
    dt = time.perf_counter() - t0
    print(f"[{dt:.2f}s] {text[:50]}... -> {translated[:50]}...")
    return JSONResponse({"translated": translated})

if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=PORT, access_log=False)
