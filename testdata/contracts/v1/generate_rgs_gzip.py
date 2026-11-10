"""Rebuild the synthetic single-member gzip cases R15 onward, without ML data."""

import gzip
import hashlib
import json
from pathlib import Path
import struct
import zlib

ROOT = Path(__file__).resolve().parent


def member(data, level=9):
    result = bytearray(gzip.compress(data, compresslevel=level, mtime=0))
    result[9] = 255  # OS-independent fixture header.
    return bytes(result)


def main():
    corpus = json.loads((ROOT / "cases.json").read_text())
    raw = (ROOT / "rgs/R01.rgs").read_bytes()
    columns = raw[32:]  # R01 contains exactly one primitive.
    header = b"RANGL4Z1" + raw[8:32]
    valid, empty = member(columns), member(b"")
    bad_crc = bytearray(valid)
    bad_crc[-8] ^= 1
    bad_empty = bytearray(empty)
    bad_empty[-8] ^= 1
    bad_size = bytearray(valid)
    bad_size[-4] ^= 1
    optional_header = bytearray(valid[:10])
    optional_header[3] = 1 | 2 | 4 | 8 | 16
    optional_header += b"\x03\0abcfixture.rgs\0fixture comment\0"
    optional = bytes(optional_header) + struct.pack("<H", zlib.crc32(optional_header) & 65535) + valid[10:]
    bad_header_crc = bytearray(optional)
    bad_header_crc[len(optional_header)] ^= 1
    unterminated = bytearray(valid[:10])
    unterminated[3] = 8
    unterminated += b"name without NUL"
    reserved = bytearray(valid)
    reserved[3] |= 128
    extra = bytearray(valid)
    extra[3] |= 4
    extra[10:10] = b"\xff\xff"
    embedded = bytearray(raw)
    embedded[32+24:32+27] = b"\x1f\x8b\x08"
    nan = bytearray(columns)
    struct.pack_into("<f", nan, 0, float("nan"))
    bomb = member(bytes((32 << 20) + 1))
    forged_bomb = bomb[:-4] + struct.pack("<I", len(columns))
    cases = [
        (15, valid + empty, False, "追加の有効な空 gzip member を拒否"),
        (16, empty + valid, False, "先頭の空 gzip member を拒否"),
        (17, member(columns[:28]) + member(columns[28:]), False, "合計展開長が一致しても分割した複数 member を拒否"),
        (18, valid + empty * 1024, False, "後方に反復する空 member を拒否"),
        (19, empty * 1024 + valid, False, "前方に反復する空 member を拒否"),
        (20, valid + b"\0", False, "末尾の NUL padding も余剰 byte として拒否"),
        (21, valid + b"\0garbage", False, "NUL の後ろの非 gzip 余剰も拒否"),
        (22, bytes(bad_crc), False, "単一 member の不正 CRC32 を拒否"),
        (23, valid + bytes(bad_empty), False, "不正 CRC の追加空 member を拒否"),
        (24, bytes(bad_size), False, "gzip ISIZE 不一致を拒否"),
        (25, optional, True, "FTEXT/FEXTRA/FNAME/FCOMMENT と正しい FHCRC を受理"),
        (26, bytes(bad_header_crc), False, "不正な FHCRC を拒否"),
        (27, bytes(unterminated), False, "終端のない FNAME を拒否"),
        (28, valid[:-9], False, "切れた deflate と trailer を拒否"),
        (29, member(columns[:-1]), False, "宣言より短い展開を拒否"),
        (30, bomb, False, "32 MiB を超える展開を拒否"),
        (31, member(bytes(embedded[32:]), level=0), True, "deflate 内の gzip magic byte 列を member 境界と誤認しない", bytes(embedded)),
        (32, valid + valid, False, "追加の非空 member と過大な総展開を拒否"),
        (33, member(bytes(nan)), False, "gzip 復号後も primitive の NaN を拒否"),
        (34, bytes(reserved), False, "gzip の予約 flag を拒否"),
        (35, bytes(valid[:3]) + b"\x18" + valid[4:10] + b"\0\0" + valid[10:], True, "空の FNAME と FCOMMENT を受理"),
        (37, bytes(extra), False, "入力を超える FEXTRA 長を拒否"),
        (38, forged_bomb, False, "ISIZE を偽装しても過大展開を宣言長の直後で打ち切る"),
    ]
    new = []
    for number, body, accept, description, *decoded in cases:
        ident = f"R{number:02}"
        (ROOT / f"rgs/{ident}.rgs").write_bytes(header + body)
        case = dict(id=ident, profile="rgs", file=f"rgs/{ident}.rgs", accept=accept,
                    context=dict(frameCount=3, frameRate=25), description=description)
        if accept:
            case["normalized"] = dict(fps=25.0, end=.08, raw_sha256=hashlib.sha256(decoded[0] if decoded else raw).hexdigest())
        new.append(case)
    new.append(dict(id="R36", profile="rgs", file="rgs/R02.rgs", accept=False,
                    recipe=dict(kind="pad-to-size", bytes=(32 << 20)+1),
                    context=dict(frameCount=3, frameRate=25), description="32 MiB + 1 wire を復号前に拒否"))
    existing = [c for c in corpus["cases"] if c["profile"] != "rgs" or int(c["id"][1:]) < 15]
    position = next(i for i, c in enumerate(existing) if c["profile"] != "rgs")
    corpus["cases"] = existing[:position] + sorted(new, key=lambda c: c["id"]) + existing[position:]
    (ROOT / "cases.json").write_text(json.dumps(corpus, ensure_ascii=False, indent=2) + "\n")


if __name__ == "__main__":
    main()
