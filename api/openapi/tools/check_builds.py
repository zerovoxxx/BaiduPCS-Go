#!/usr/bin/env python3
"""Compile the original CLI target union and verify SQLite build isolation."""
import argparse
import concurrent.futures
import json
import os
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[3]
TARGETS = [('darwin','arm64'),('darwin','amd64'),('linux','amd64'),('linux','arm64'),('windows','amd64'),('linux','386'),('linux','arm'),('linux','mips'),('linux','mipsle'),('linux','mips64'),('linux','mips64le'),('windows','386'),('windows','arm')]
SERVER_TARGETS = set(TARGETS[:5])


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--report',type=Path,help='optional JSON report; binaries are always temporary')
    args=parser.parse_args()
    with tempfile.TemporaryDirectory(prefix='baidupcs-p1-builds-') as directory:
        def build(target):
            goos,goarch=target
            env={**os.environ,'GOTOOLCHAIN':'go1.23.12','CGO_ENABLED':'0','GOOS':goos,'GOARCH':goarch,'GOARM':'7'}
            if goarch in ['mips','mipsle']:env['GOMIPS']='softfloat'
            process=subprocess.run(['go','build','-trimpath','-o',str(Path(directory)/(goos+'-'+goarch)),'.'],cwd=ROOT,env=env,text=True,capture_output=True)
            deps=subprocess.run(['go','list','-deps','.'],cwd=ROOT,env=env,text=True,capture_output=True)
            linked='modernc.org/sqlite' in deps.stdout.splitlines()
            row={'target':goos+'/'+goarch,'exit_code':process.returncode,'sqlite_linked':linked,'expected_sqlite':target in SERVER_TARGETS,'errors':(process.stderr+deps.stderr)[-3000:],'dependency_exit_code':deps.returncode}
            print(json.dumps(row),flush=True)
            return row
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            results=list(pool.map(build,TARGETS))
    if args.report:
        args.report.write_text(json.dumps(results,indent=2)+'\n')
    if any(row['exit_code'] or row['dependency_exit_code'] or row['sqlite_linked']!=row['expected_sqlite'] for row in results):
        raise SystemExit(1)


if __name__=='__main__':
    main()
