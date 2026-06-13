#!/usr/bin/env python3
import argparse
import os
from pathlib import Path


def write_random_file(path: Path, size: int, executable: bool = False) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)

    with path.open("wb") as f:
        f.write(os.urandom(size))

    if executable:
        # Owner/group/other executable, readable/writable by owner.
        path.chmod(0o755)


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Write small arbitrary binary files filled with random bytes."
    )

    parser.add_argument(
        "output",
        help="Output file path, or output directory when using --count > 1",
    )

    parser.add_argument(
        "-s",
        "--size",
        type=int,
        default=None,
        help="Exact file size in bytes",
    )

    parser.add_argument(
        "--min-size",
        type=int,
        default=1,
        help="Minimum random file size in bytes, default: 1",
    )

    parser.add_argument(
        "--max-size",
        type=int,
        default=2048,
        help="Maximum random file size in bytes, default: 2048",
    )

    parser.add_argument(
        "-n",
        "--count",
        type=int,
        default=1,
        help="Number of files to create, default: 1",
    )

    parser.add_argument(
        "-x",
        "--executable",
        action="store_true",
        help="Mark generated files as executable on Unix-like systems",
    )

    parser.add_argument(
        "--prefix",
        default="blob",
        help="Filename prefix when creating multiple files, default: blob",
    )

    args = parser.parse_args()

    if args.count < 1:
        raise SystemExit("count must be >= 1")

    if args.size is not None:
        if args.size < 0:
            raise SystemExit("size must be >= 0")
    else:
        if args.min_size < 0:
            raise SystemExit("min-size must be >= 0")
        if args.max_size < args.min_size:
            raise SystemExit("max-size must be >= min-size")

    output = Path(args.output)

    for i in range(args.count):
        if args.size is None:
            size = args.min_size + int.from_bytes(
                os.urandom(8), "big"
            ) % (args.max_size - args.min_size + 1)
        else:
            size = args.size

        if args.count == 1:
            path = output
        else:
            path = output / f"{args.prefix}_{i:04d}_{size}b.bin"

        write_random_file(path, size, args.executable)
        print(f"Wrote {path} ({size} bytes)")


if __name__ == "__main__":
    main()

# One file, random size between 1 and 2048 bytes
# python3 ./scripts/gen-bin.py sample.bin

# One exact 16-byte file
# python3 ./scripts/gen-bin.py tiny.bin --size 16

# 20 files, each between 1 and 512 bytes
# python3 ./scripts/gen-bin.py outdir --count 20 --min-size 1 --max-size 512

# Random executable-looking blobs with executable bit set
#python3 ./scripts/gen-bin.py fuzz_inputs --count 10 --max-size 2048 --executable
