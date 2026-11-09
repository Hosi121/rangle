# Build from the repository root. The dependencies target needs no worker source.
# Both official PyTorch images pin Python 3.11 / Torch 2.6 / CUDA 12.4.
FROM pytorch/pytorch:2.6.0-cuda12.4-cudnn9-devel@sha256:0cf3402e946b7c384ba943ee05c90b4c5a4a05227923921f2b0918c011cfaf56 AS build

ENV TORCH_CUDA_ARCH_LIST=7.5 \
    MAX_JOBS=2 \
    PIP_DISABLE_PIP_VERSION_CHECK=1

COPY ml/requirements.lock /tmp/requirements.lock
# Resolve no dependencies here: the lock contains the complete additional graph,
# while the digest-pinned base owns Torch and its CUDA libraries. Compile gsplat
# at image build time, even on a host without a GPU, rather than on a paid job.
RUN mkdir /wheels \
    && sed '/^gsplat @ /d' /tmp/requirements.lock > /tmp/python-wheels.lock \
    && grep '^gsplat @ ' /tmp/requirements.lock > /tmp/gsplat-source.lock \
    && python -m pip download --only-binary=:all: --no-deps --require-hashes \
        --dest /wheels -r /tmp/python-wheels.lock \
    && python -m pip install --no-index --no-deps /wheels/*.whl \
    && python -m pip wheel --no-deps --no-build-isolation --require-hashes \
        --wheel-dir /wheels -r /tmp/gsplat-source.lock \
    && python -c "import torch; assert torch.__version__ == '2.6.0+cu124'; assert torch.version.cuda == '12.4'"
RUN python -c "import glob, zipfile; zipfile.ZipFile(glob.glob('/wheels/gsplat-*.whl')[0]).extract('gsplat/csrc.so', '/tmp/gsplat-kernel')" \
    && cuobjdump --list-elf /tmp/gsplat-kernel/gsplat/csrc.so | grep sm_75

FROM pytorch/pytorch:2.6.0-cuda12.4-cudnn9-runtime@sha256:77f17f843507062875ce8be2a6f76aa6aa3df7f9ef1e31d9d7432f4b0f563dee AS dependencies

ENV PYTHONPATH=/opt/rangle/ml \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    OMP_NUM_THREADS=4 \
    PIP_DISABLE_PIP_VERSION_CHECK=1

COPY --from=build /wheels /tmp/wheels
RUN python -m pip install --no-index --no-deps /tmp/wheels/*.whl \
    && rm -rf /tmp/wheels \
    && python -m pip check
RUN python - <<'PY'
import io
import numpy as np
import torch
from scipy.spatial import cKDTree
from PIL import Image
from google.cloud import storage
import gsplat.csrc
from gsplat.cuda._backend import _C

assert torch.__version__ == "2.6.0+cu124" and torch.version.cuda == "12.4"
assert _C is gsplat.csrc
array = np.ones((2, 3), dtype=np.float32)
assert torch.from_numpy(array).sum().item() == 6
assert cKDTree(array).query(array[0])[0] == 0
buffer = io.BytesIO()
Image.new("RGB", (2, 2), (1, 2, 3)).save(buffer, format="PNG")
buffer.seek(0)
assert Image.open(buffer).getpixel((0, 0)) == (1, 2, 3)
assert storage.Client
print("CPU dependencies and compiled gsplat extension loaded; GPU kernels untested")
PY

WORKDIR /tmp
USER 65532:65532

FROM golang:1.27.3-bookworm AS live-validator
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal/ ./internal/
COPY cmd/rangle-live-check/ ./cmd/rangle-live-check/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /rangle-live-check ./cmd/rangle-live-check

FROM dependencies AS live-trainer
USER root
COPY ml/rangle_live/requirements.lock /tmp/live-requirements.lock
RUN python -m pip install --no-deps --require-hashes --only-binary=:all: -r /tmp/live-requirements.lock \
    && python -m pip check && rm /tmp/live-requirements.lock
COPY --from=live-validator /rangle-live-check /usr/local/bin/rangle-live-check
COPY --chown=65532:65532 ml/rangle_training/ /opt/rangle/ml/rangle_training/
COPY --chown=65532:65532 ml/rangle_preprocessing/ /opt/rangle/ml/rangle_preprocessing/
COPY --chown=65532:65532 ml/rangle_live/ /opt/rangle/ml/rangle_live/
COPY --chown=65532:65532 scripts/publish-live.py scripts/publish-catalog.py /opt/rangle/scripts/
USER 65532:65532
ENTRYPOINT ["python", "-m", "rangle_live.cloud"]

# Keep the existing default/final image and offline entrypoint backward compatible.
FROM dependencies AS trainer
COPY --chown=65532:65532 ml/rangle_training/ /opt/rangle/ml/rangle_training/
ENTRYPOINT ["python", "-m", "rangle_training.train"]
