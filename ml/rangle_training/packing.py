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


# RGS1 stores each primitive once, in its original order, with float32 motion.
TEMPORAL_HEADER_BYTES = 32
TEMPORAL_STRIDE = 56
MAX_TEMPORAL_BYTES = 32 << 20
MAX_TEMPORAL_GAUSSIANS = 500_000
TEMPORAL = np.dtype(
    SPLAT.descr
    + [
        ("velocity", "<f4", 3),
        ("centre_time", "<f4"),
        ("sigma", "<f4"),
        ("peak", "<f4"),
    ]
)


def temporal_header(payload):
    import struct

    if not TEMPORAL_HEADER_BYTES <= len(payload) <= MAX_TEMPORAL_BYTES:
        raise ValueError("Invalid temporal model size")
    magic, count, stride, frames, fps, duration, flags = struct.unpack_from(
        "<8sIIIffI", payload
    )
    compressed = magic == b"RANGL4Z1"
    raw_size = TEMPORAL_HEADER_BYTES + count * TEMPORAL_STRIDE
    if (
        magic not in (b"RANGL4D1", b"RANGL4Z1")
        or not 1 <= count <= MAX_TEMPORAL_GAUSSIANS
        or stride != TEMPORAL_STRIDE
        or not 2 <= frames <= 32
        or not np.isfinite(fps)
        or not 0 < fps <= 240
        or not np.isfinite(duration)
        or duration <= 0
        or abs(duration - (frames - 1) / fps) > 1e-5
        or flags != 0
        or raw_size > MAX_TEMPORAL_BYTES
        or (not compressed and len(payload) != raw_size)
        or (compressed and len(payload) <= TEMPORAL_HEADER_BYTES)
    ):
        raise ValueError("Invalid temporal model header")
    return count, frames, fps, duration, compressed


def pack_temporal(
    means,
    scales,
    quats,
    colors,
    peak,
    velocity,
    centre_time,
    sigma,
    *,
    frame_count,
    frame_rate,
):
    """Pack the existing RGS1 layout without alpha culling or ID reordering."""
    import struct

    count = len(means)
    if not 1 <= count <= MAX_TEMPORAL_GAUSSIANS:
        raise ValueError("Temporal Gaussian budget exceeded")
    for array, shape in (
        (peak, (count,)),
        (velocity, (count, 3)),
        (centre_time, (count,)),
        (sigma, (count,)),
    ):
        if array.shape != shape or not np.isfinite(array).all():
            raise ValueError("Invalid temporal parameter shape or values")
    if (sigma <= 0).any() or ((peak < 0) | (peak > 1)).any():
        raise ValueError("Invalid temporal opacity or duration")
    # A nonzero temporary alpha makes pack_splats preserve even currently invisible IDs.
    static = np.frombuffer(
        pack_splats(means, scales, quats, colors, np.ones(count)), dtype=SPLAT
    )
    rows = np.empty(count, dtype=TEMPORAL)
    for name in SPLAT.names:
        rows[name] = static[name]
    rows["rgba"][:, 3] = np.rint(peak * 255).astype(np.uint8)
    rows["velocity"], rows["centre_time"] = velocity, centre_time
    rows["sigma"], rows["peak"] = sigma, peak
    header = struct.pack(
        "<8sIIIffI",
        b"RANGL4D1",
        count,
        TEMPORAL_STRIDE,
        frame_count,
        frame_rate,
        (frame_count - 1) / frame_rate,
        0,
    )
    result = header + rows.tobytes()
    # Validate after float32 conversion, including representability of reciprocal sigma.
    unpack_temporal(result)
    return result


def compress_temporal(payload):
    """Lossless byte-column shuffle + gzip, identical to the viewer codec."""
    import gzip

    count, _, _, _, compressed = temporal_header(payload)
    if compressed:
        raise ValueError("Temporal model is already compressed")
    columns = np.frombuffer(payload, dtype=np.uint8, offset=TEMPORAL_HEADER_BYTES)
    columns = columns.reshape(count, TEMPORAL_STRIDE).T.copy().tobytes()
    result = (
        b"RANGL4Z1" + payload[8:TEMPORAL_HEADER_BYTES] + gzip.compress(columns, mtime=0)
    )
    temporal_header(result)
    return result


def compress_temporal_rle(payload):
    """Opt-in Z1 gzip strategy for measured assets, preserving every raw byte.

    RLE can be larger on dictionary-friendly input. Keep the existing default
    encoder unchanged and compare complete asset sizes before publication.
    """
    import zlib

    count, _, _, _, compressed = temporal_header(payload)
    if compressed:
        raise ValueError("Temporal model is already compressed")
    rows = np.frombuffer(payload, dtype=np.uint8, offset=TEMPORAL_HEADER_BYTES)
    rows = rows.reshape(count, TEMPORAL_STRIDE)
    # RLE retains the existing Z1 gzip stream. Feed one column at a time to
    # avoid materializing two extra full-size copies of the shuffled records.
    encoder = zlib.compressobj(level=9, wbits=31, strategy=zlib.Z_RLE)
    parts = [b"RANGL4Z1", payload[8:TEMPORAL_HEADER_BYTES]]
    for column in rows.T:
        parts.append(encoder.compress(column.tobytes()))
    parts.append(encoder.flush())
    result = b"".join(parts)
    temporal_header(result)
    return result


def compress_temporal_smallest(payload):
    """Choose the smaller Z1 stream; ties retain the existing default encoder.

    Both strategies run, so the RLE-only CPU/memory gains do not apply here.
    """
    original = compress_temporal(payload)
    rle = compress_temporal_rle(payload)
    return rle if len(rle) < len(original) else original


def decode_temporal(payload):
    import zlib

    count, _, _, _, compressed = temporal_header(payload)
    if not compressed:
        return payload
    expected = count * TEMPORAL_STRIDE
    # One native gzip member, including its CRC/ISIZE, must consume all input.
    # GzipFile would silently accept extra members and trailing NUL padding.
    decoder = zlib.decompressobj(wbits=31)
    try:
        columns = decoder.decompress(payload[TEMPORAL_HEADER_BYTES:], expected + 1)
    except zlib.error as error:
        raise ValueError("Invalid temporal gzip checksum or stream") from error
    if (
        len(columns) != expected
        or not decoder.eof
        or decoder.unused_data
        or decoder.unconsumed_tail
    ):
        raise ValueError("Invalid temporal gzip size, termination or trailing bytes")
    rows = (
        np.frombuffer(columns, dtype=np.uint8).reshape(TEMPORAL_STRIDE, count).T.copy()
    )
    return b"RANGL4D1" + payload[8:TEMPORAL_HEADER_BYTES] + rows.tobytes()


def unpack_temporal(payload):
    """Decode stored parameters; render alpha is rounded at the requested time."""
    raw = decode_temporal(payload)
    rows = np.frombuffer(raw, dtype=TEMPORAL, offset=TEMPORAL_HEADER_BYTES)
    for field in ("position", "scale", "velocity", "centre_time", "sigma", "peak"):
        if not np.isfinite(rows[field]).all():
            raise ValueError("Nonfinite temporal primitive")
    with np.errstate(over="ignore", divide="ignore"):
        reciprocal = np.float32(1) / rows["sigma"]
    if (
        (rows["scale"] <= 0).any()
        or (rows["sigma"] <= 0).any()
        or not np.isfinite(reciprocal).all()
        or ((rows["peak"] < 0) | (rows["peak"] > 1)).any()
    ):
        raise ValueError("Invalid temporal primitive")
    static_rows = np.empty(len(rows), dtype=SPLAT)
    for name in SPLAT.names:
        static_rows[name] = rows[name]
    means, scales, quats, colors, _ = unpack_splats(static_rows.tobytes())
    return (
        means,
        scales,
        quats,
        colors,
        rows["peak"].copy(),
        rows["velocity"].copy(),
        rows["centre_time"].copy(),
        rows["sigma"].copy(),
    )
