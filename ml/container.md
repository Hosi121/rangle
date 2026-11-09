# Vertex trainer container

Build from the repository root for Linux x86_64. The `dependencies` target is independent of the worker source:

```sh
docker build --platform=linux/amd64 --target dependencies -f ml/Dockerfile -t rangle-trainer-deps:local .
docker run --rm --network=none --read-only --tmpfs /tmp:rw,nosuid,nodev,size=64m \
  --entrypoint python rangle-trainer-deps:local -c \
  'import torch, gsplat.csrc; from gsplat.cuda._backend import _C; assert _C is gsplat.csrc; print(torch.__version__, torch.version.cuda)'
```

After the reviewed `ml/rangle_training/` source is present, build the final `trainer` target. Its entrypoint is `python -m rangle_training.train`; append the approved job arguments. Push a candidate using its source commit tag, then pin the returned digest in the private generation config for one bounded T4 validation job. Keep the attempt limit at one until that job has been checked.

```sh
docker build --platform=linux/amd64 --target trainer -f ml/Dockerfile -t rangle-trainer:local .
docker run --rm --network=none rangle-trainer:local --help
```

Both official PyTorch bases are pinned by manifest digest and contain CPython 3.11.11, Torch 2.6.0+cu124 and CUDA 12.4. Additional pins include gsplat 1.5.3, NumPy 2.4.6, SciPy 1.17.1, Pillow 13.0.0 and google-cloud-storage 3.17.1. `requirements.lock` freezes the added dependency graph and each artifact SHA256, checked against [PyPI release metadata](https://docs.pypi.org/api/json/). Torch and CUDA remain supplied by the base; pip uses `--no-deps` and never resolves a replacement Torch. Build tools (including setuptools 81) are pinned too.

The requested Torch 2.6 / CUDA 12.4 wheel index returned 404, and the official [gsplat v1.5.3 assets](https://github.com/nerfstudio-project/gsplat/releases/tag/v1.5.3) do not include that CPython 3.11 ABI. The build stage compiles the hash-pinned PyPI source with `TORCH_CUDA_ARCH_LIST=7.5` and `MAX_JOBS=2`, following the upstream [source build](https://github.com/nerfstudio-project/gsplat/blob/v1.5.3/setup.py). `cuobjdump` checks that the resulting extension includes `sm_75`; NVIDIA lists [T4 as compute capability 7.5](https://developer.nvidia.com/cuda/gpus). [PyTorch 2.6 officially supports CUDA 12.4](https://pytorch.org/get-started/previous-versions/).

The runtime stage imports the compiled extension and asserts that gsplat actually uses it, preventing a successful build with a missing extension that would require paid cold-start JIT. CUDA compiler tools remain in the build stage; runtime uses the official runtime base and retains the package headers supplied by it. The final image runs as UID/GID 65532, uses `/tmp` for work, and copies only worker Python source. No dataset, config, credentials or gcloud SDK is included. Google Storage uses the job service account through ADC; do not mount a user credential file.

The final target includes the worker and has passed `--help` and its five CPU contract tests as UID 65532, without network access and with a read-only filesystem. Build checks cover NumPy-to-Torch conversion, cKDTree query, Pillow PNG encode/decode, Storage import, extension loading and package consistency. A separate local smoke repeats these as UID 65532 with a read-only filesystem and no network. GPU kernel execution, driver compatibility, training quality, duration and spend require the bounded real T4 test before enabling further attempts. Private offline validation remains separate and has no container, GCS or live-service integration here.
