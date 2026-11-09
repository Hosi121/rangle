"""Pack and safely extract one checksummed, bounded calibrated video source."""

import argparse
import hashlib
import json
import shutil
import stat
import tempfile
import zipfile
from pathlib import Path, PurePosixPath

from rangle_preprocessing.schema import _pairs, _reject_constant
from rangle_preprocessing.sources import load_seed

from .output import json_bytes
from .source import Rig

MAX_BYTES = 1 << 30
MAX_FILES = 256
MANIFEST_BYTES = 1 << 20


def safe_name(name):
    path = PurePosixPath(name)
    if (
        not name
        or name == "."
        or len(name) > 512
        or path.is_absolute()
        or ".." in path.parts
        or name != path.as_posix()
        or "\\" in name
        or any(ord(char) < 32 or ord(char) == 127 for char in name)
    ):
        raise ValueError("Unsafe source bundle path")
    return name


def document(body):
    return json.loads(body, object_pairs_hook=_pairs, parse_constant=_reject_constant)


def pack(manifest, source_root, output):
    rig = Rig(manifest, source_root)
    load_seed(
        rig.seed_path, rig.config["seed"]["max_points"], rig.config["seed"]["seed"]
    )
    paths = {
        record["path"]: rig.root / record["path"]
        for record in rig.provenance["geometry_files"]
    }
    paths.update(
        {path.relative_to(rig.root).as_posix(): path for path in rig.video_paths}
    )
    if len(paths) + 2 > MAX_FILES or {"source.json", "bundle.json"} & paths.keys():
        raise ValueError("Source file count or reserved path conflict")
    for name in paths:
        safe_name(name)
    if sum(path.stat().st_size for path in paths.values()) > MAX_BYTES:
        raise ValueError("Source exceeds 1 GiB unpacked")
    output = Path(output)
    records = []
    try:
        with zipfile.ZipFile(
            output, "x", compression=zipfile.ZIP_STORED, allowZip64=False
        ) as archive:
            source = json_bytes(rig.config)
            archive.writestr(zipfile.ZipInfo("source.json"), source)
            records.append(
                {
                    "path": "source.json",
                    "bytes": len(source),
                    "sha256": hashlib.sha256(source).hexdigest(),
                }
            )
            for name, path in sorted(paths.items()):
                before = path.stat()
                if not stat.S_ISREG(before.st_mode):
                    raise ValueError("Source must contain regular files")
                digest, count = hashlib.sha256(), 0
                with path.open("rb") as stream, archive.open(name, "w") as target:
                    while block := stream.read(4 << 20):
                        count += len(block)
                        if count > before.st_size:
                            raise ValueError("Source changed during packing")
                        digest.update(block)
                        target.write(block)
                after = path.stat()
                if (
                    count != before.st_size
                    or before.st_mtime_ns != after.st_mtime_ns
                    or before.st_ino != after.st_ino
                ):
                    raise ValueError("Source changed during packing")
                records.append(
                    {"path": name, "bytes": count, "sha256": digest.hexdigest()}
                )
            body = json_bytes({"version": 1, "files": records})
            if (
                len(body) > MANIFEST_BYTES
                or sum(row["bytes"] for row in records) + len(body) > MAX_BYTES
            ):
                raise ValueError("Source manifest or unpacked byte budget exceeded")
            archive.writestr(zipfile.ZipInfo("bundle.json"), body)
        if output.stat().st_size > MAX_BYTES:
            raise ValueError("Source bundle exceeds 1 GiB compressed")
    except FileExistsError:
        raise
    except BaseException:
        output.unlink(missing_ok=True)
        raise
    with output.open("rb") as stream:
        checksum = hashlib.file_digest(stream, "sha256").hexdigest()
    return {
        "version": 1,
        "input_sha256": checksum,
        "bytes": output.stat().st_size,
        "files": len(records),
        "unpacked_bytes": sum(row["bytes"] for row in records) + len(body),
    }


def extract(bundle, destination):
    bundle, destination = Path(bundle), Path(destination)
    if destination.exists():
        raise FileExistsError("Source extraction never overwrites a directory")
    if bundle.stat().st_size > MAX_BYTES:
        raise ValueError("Source bundle exceeds 1 GiB compressed")
    temporary = Path(tempfile.mkdtemp(prefix=".source-", dir=destination.parent))
    try:
        with zipfile.ZipFile(bundle) as archive:
            entries = archive.infolist()
            names = [safe_name(entry.filename) for entry in entries]
            if (
                len(entries) > MAX_FILES
                or len(names) != len(set(names))
                or "bundle.json" not in names
                or "source.json" not in names
                or sum(entry.file_size for entry in entries) > MAX_BYTES
            ):
                raise ValueError("Source bundle files or expansion budget invalid")
            for entry in entries:
                mode = stat.S_IFMT(entry.external_attr >> 16)
                if (
                    entry.is_dir()
                    or mode not in (0, stat.S_IFREG)
                    or entry.flag_bits & 1
                    or entry.compress_type
                    not in (zipfile.ZIP_STORED, zipfile.ZIP_DEFLATED)
                ):
                    raise ValueError(
                        "Only regular unencrypted source members are accepted"
                    )
            if archive.getinfo("bundle.json").file_size > MANIFEST_BYTES:
                raise ValueError("Source manifest exceeds budget")
            metadata = document(archive.read("bundle.json"))
            if (
                not isinstance(metadata, dict)
                or set(metadata) != {"version", "files"}
                or type(metadata["version"]) is not int
                or metadata["version"] != 1
                or not isinstance(metadata["files"], list)
            ):
                raise ValueError("Invalid source manifest")
            expected = {}
            for row in metadata["files"]:
                if (
                    not isinstance(row, dict)
                    or set(row) != {"path", "bytes", "sha256"}
                    or not isinstance(row["path"], str)
                    or type(row["bytes"]) is not int
                    or not 0 <= row["bytes"] <= MAX_BYTES
                    or not isinstance(row["sha256"], str)
                    or len(row["sha256"]) != 64
                    or any(c not in "0123456789abcdef" for c in row["sha256"])
                ):
                    raise ValueError("Invalid source file proof")
                name = safe_name(row["path"])
                if name in expected or name == "bundle.json":
                    raise ValueError("Duplicate or reserved source proof")
                expected[name] = row
            if set(expected) != set(names) - {"bundle.json"}:
                raise ValueError("Source manifest differs from archive members")
            for name, proof in expected.items():
                entry = archive.getinfo(name)
                if entry.file_size != proof["bytes"]:
                    raise ValueError("Source size differs from proof")
                path = temporary / name
                path.parent.mkdir(parents=True, exist_ok=True)
                digest, count = hashlib.sha256(), 0
                with archive.open(entry) as source, path.open("xb") as target:
                    while block := source.read(4 << 20):
                        count += len(block)
                        if count > proof["bytes"]:
                            raise ValueError("Source expansion exceeds declared size")
                        digest.update(block)
                        target.write(block)
                if count != proof["bytes"] or digest.hexdigest() != proof["sha256"]:
                    raise ValueError("Source checksum differs from proof")
        rig = Rig(temporary / "source.json", temporary)
        load_seed(
            rig.seed_path, rig.config["seed"]["max_points"], rig.config["seed"]["seed"]
        )
        # Claim the private destination exclusively; rename alone can replace an
        # empty directory created by another process during validation.
        destination.mkdir(mode=0o700)
        try:
            for item in temporary.iterdir():
                item.rename(destination / item.name)
        except BaseException:
            shutil.rmtree(destination)
            raise
    finally:
        if temporary.exists():
            shutil.rmtree(temporary)
    return destination / "source.json"


def main(argv=None):
    cli = argparse.ArgumentParser(description=__doc__)
    sub = cli.add_subparsers(dest="command", required=True)
    command = sub.add_parser("pack")
    command.add_argument("--source-manifest", type=Path, required=True)
    command.add_argument("--source-root", type=Path, required=True)
    command.add_argument("--output", type=Path, required=True)
    args = cli.parse_args(argv)
    print(
        json.dumps(
            pack(args.source_manifest, args.source_root, args.output), allow_nan=False
        )
    )


if __name__ == "__main__":
    main()
