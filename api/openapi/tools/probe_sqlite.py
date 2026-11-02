#!/usr/bin/env python3
"""Optional isolated storage compatibility probe; downloads Go/module dependencies."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile

TOOLS = Path(__file__).resolve().parent
TARGETS = [('darwin', 'arm64'), ('darwin', 'amd64'), ('linux', 'amd64'), ('linux', 'arm64'), ('windows', 'amd64')]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--cross', action='store_true', help='also compile the five initial server targets, plus the known unsupported linux/mipsle target')
    args = parser.parse_args()
    env = {**os.environ, 'GOTOOLCHAIN': 'go1.23.12', 'CGO_ENABLED': '0'}
    # Do not inherit a caller's cross-compilation target for the native run.
    env.pop('GOOS', None)
    env.pop('GOARCH', None)
    env.pop('GOWORK', None)
    env['GOWORK'] = 'off'
    with tempfile.TemporaryDirectory(prefix='baidupcs-http-p0-sqlite-') as directory:
        work = Path(directory)
        (work / 'go.mod').write_text('module baidupcs-http-p0-sqlite-probe\n\ngo 1.23.0\n\nrequire modernc.org/sqlite v1.38.2\n')
        (work / 'main.go').write_text((TOOLS / 'sqlite-probe.go.txt').read_text())
        subprocess.run(['go', 'mod', 'tidy'], cwd=work, env=env, check=True)
        subprocess.run(['go', 'run', '.'], cwd=work, env=env, check=True)
        if args.cross:
            for goos, goarch in TARGETS + [('linux', 'mipsle')]:
                result = subprocess.run(['go', 'build', '-o', str(work / f'probe-{goos}-{goarch}'), '.'], cwd=work, env={**env, 'GOOS': goos, 'GOARCH': goarch}, capture_output=True, text=True)
                expected_success = (goos, goarch) in TARGETS
                print(json.dumps({'target': goos + '/' + goarch, 'compiled': result.returncode == 0, 'expected': expected_success}), flush=True)
                if (result.returncode == 0) != expected_success:
                    raise RuntimeError(result.stderr or 'unexpected platform support change; revisit the P0 platform matrix')
                if not expected_success and ('modernc.org/libc' not in result.stderr or 'build constraints exclude all Go files' not in result.stderr):
                    raise RuntimeError('unsupported target failed for an unexpected reason: ' + result.stderr)


if __name__ == '__main__':
    main()
