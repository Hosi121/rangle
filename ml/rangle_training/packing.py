"""The viewer's existing 32-byte .splat format; gzip transport is lossless.

RGBA and quaternion storage use the format's existing 8-bit quantization. This
packing is not claimed to reproduce the floating-point training model exactly.
"""

import numpy as np

SPLAT = np.dtype(
    [
        ("position", "<f4", 3),
        ("scale", "<f4", 3),
        ("rgba", "u1", 4),
        ("quaternion", "u1", 4),
    ]
)


def pack_splats(means, scales, quats, colors, opacity) -> bytes:
    count = len(means)
    for array, shape in (
        (means, (count, 3)),
        (scales, (count, 3)),
        (quats, (count, 4)),
        (colors, (count, 3)),
        (opacity, (count,)),
    ):
        if array.shape != shape or not np.isfinite(array).all():
            raise ValueError("Invalid model shape or nonfinite values")
    if (
        count == 0
        or (scales <= 0).any()
        or (np.linalg.norm(quats, axis=1) < 1e-8).any()
    ):
        raise ValueError("Invalid Gaussian scale or orientation")
    result = np.empty(count, dtype=SPLAT)
    result["position"] = means
    result["scale"] = scales
    result["rgba"][:, :3] = np.rint(np.clip(colors, 0, 1) * 255).astype(np.uint8)
    result["rgba"][:, 3] = np.rint(np.clip(opacity, 0, 1) * 255).astype(np.uint8)
    normalized = quats / np.linalg.norm(quats, axis=1, keepdims=True)
    result["quaternion"] = np.rint(normalized * 128 + 128).clip(0, 255).astype(np.uint8)
    # Removing only alpha==0 in the stored representation does not change its drawing.
    return result[result["rgba"][:, 3] != 0].tobytes()


def unpack_splats(payload: bytes) -> tuple:
    if not payload or len(payload) % SPLAT.itemsize:
        raise ValueError("Invalid splat payload length")
    points = np.frombuffer(payload, dtype=SPLAT)
    quats = (points["quaternion"].astype(np.float32) - 128) / 128
    quats /= np.maximum(np.linalg.norm(quats, axis=1, keepdims=True), 1e-8)
    return (
        points["position"].copy(),
        points["scale"].copy(),
        quats,
        points["rgba"][:, :3].astype(np.float32) / 255,
        points["rgba"][:, 3].astype(np.float32) / 255,
    )
