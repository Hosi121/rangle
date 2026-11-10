# Vertex trainer container

Build from repository root for Linux x86_64. `dependencies` is independent of worker source:

```sh
docker build --platform=linux/amd64 --target dependencies -f ml/Dockerfile -t rangle-trainer-deps:local .
docker run --rm --network=none --read-only --tmpfs /tmp:rw,nosuid,nodev,size=64m \
  --entrypoint python rangle-trainer-deps:local -c \
  'import torch, gsplat.csrc; from gsplat.cuda._backend import _C; assert _C is gsplat.csrc; print(torch.__version__, torch.version.cuda)'
```

With reviewed `ml/rangle_training/` source, build `trainer` (entrypoint `python -m rangle_training.train`) and append approved job arguments:

```sh
docker build --platform=linux/amd64 --target trainer -f ml/Dockerfile -t rangle-trainer:local .
docker run --rm --network=none rangle-trainer:local --help
```

Push a source-commit-tagged candidate, pin its digest in private generation config and validate one bounded T4 job before permitting further attempts. Workflow: [generation](../docs/generation.md).

| Layer | Pins / checks |
| --- | --- |
| Base | Official PyTorch build/runtime manifest digests; CPython 3.11.11, Torch 2.6.0+cu124, CUDA 12.4. Torch/CUDA come from the base; pip `--no-deps` cannot replace Torch. |
| Added packages | gsplat 1.5.3, NumPy 2.4.6, SciPy 1.17.1, Pillow 13.0.0, google-cloud-storage 3.17.1; complete graph and SHA256 in `requirements.lock`, checked against [PyPI metadata](https://docs.pypi.org/api/json/). Build tools include pinned setuptools 81. |
| CUDA extension | Requested Torch 2.6/CUDA 12.4 wheel index returned 404; [v1.5.3 assets](https://github.com/nerfstudio-project/gsplat/releases/tag/v1.5.3) lack this CPython 3.11 ABI. Compile hash-pinned source per [upstream setup](https://github.com/nerfstudio-project/gsplat/blob/v1.5.3/setup.py), `TORCH_CUDA_ARCH_LIST=7.5`, `MAX_JOBS=2`; `cuobjdump` checks `sm_75`. [T4 capability 7.5](https://developer.nvidia.com/cuda/gpus), [Torch 2.6/CUDA 12.4 support](https://pytorch.org/get-started/previous-versions/). |

Runtime asserts gsplat uses the compiled extension, avoiding missing-extension cold-start JIT. Compiler tools stay in build stage; runtime retains base-supplied headers. UID/GID 65532, `/tmp` work, worker source only; no dataset/config/credentials/gcloud. Storage uses job-service-account ADC; do not mount user credentials.

Recorded validation: `--help` and five CPU contracts passed as UID 65532 with read-only filesystem/no network. Build and separate runtime smoke check NumPy→Torch, cKDTree, Pillow PNG, Storage import, extension loading and package consistency. GPU kernels/driver/quality/duration/spend require the bounded T4 test; private offline validation is separate from container/GCS/live integration.
