#!/usr/bin/env python3
"""Exercise stopped backup, restore and actual schema-v6 binary refusal.

Uses only private fixture data, loopback HTTP and temporary source under this
worktree. Does not read .env, CLI credentials, or contact Baidu.
"""
import argparse
import hashlib
import io
import json
import os
import platform
from datetime import datetime, timezone
from pathlib import Path
import shutil
import signal
import sqlite3
import socket
import subprocess
import sys
import tarfile
import tempfile
import time
from unittest.mock import patch
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

sys.dont_write_bytecode = True
import backup_service as backup

ROOT = Path(__file__).resolve().parents[3]
OLD = '83f6a83'
TOKEN = 'P7_RECOVERY_FIXTURE_ONLY_TOKEN_0123456789abcdef'


def run(*args, cwd=ROOT):
    return subprocess.run(args, cwd=cwd, check=True, capture_output=True, timeout=300)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--report', type=Path)
    args = parser.parse_args()
    rows = []
    def passed(name):
        rows.append({'case': name, 'status': 'passed'})
        print('PASS: ' + name, flush=True)
    with tempfile.TemporaryDirectory(prefix='.p7-compat-', dir=ROOT) as temp:
        temp = Path(temp)
        temp.chmod(0o700)
        current, old = temp/'current', temp/'schema-v6'
        run('go','build','-trimpath','-o',str(current),'.')
        source = temp/'old-source'; source.mkdir(mode=0o700)
        archive = run('git','archive',OLD).stdout
        with tarfile.open(fileobj=io.BytesIO(archive)) as tar:
            # A trusted repository commit, checked for traversal nevertheless.
            for member in tar.getmembers():
                if member.issym() or member.islnk() or Path(member.name).is_absolute() or '..' in Path(member.name).parts:
                    raise ValueError('unsafe archived source')
            tar.extractall(source)
        run('go','build','-trimpath','-o',str(old),'.',cwd=source)
        passed('build_current_and_actual_schema_v6_binary')
        token = temp/'token'; token.write_text(TOKEN); token.chmod(0o600)
        data = temp/'data'
        process = None
        def start(binary, directory):
            nonlocal process
            directory.mkdir(mode=0o700, exist_ok=True)
            directory.chmod(0o700)
            with socket.socket() as sock:
                sock.bind(('127.0.0.1',0)); port=sock.getsockname()[1]
            base='http://127.0.0.1:'+str(port)
            process = subprocess.Popen([str(binary),'server','--data-dir',str(directory),'--listen','127.0.0.1:'+str(port),'--token-file',str(token)], cwd=ROOT, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
            deadline=time.monotonic()+12
            while time.monotonic()<deadline:
                if process.poll() is not None:
                    raise ValueError('fixture service failed to start: '+process.communicate()[1].decode()[-1000:])
                try:
                    with urlopen(base+'/healthz',timeout=0.5) as res:
                        if res.status==200: return base
                except (URLError,TimeoutError): pass
                time.sleep(0.03)
            raise ValueError('fixture service startup timed out')
        def stop():
            nonlocal process
            if process is not None and process.poll() is None:
                process.send_signal(signal.SIGTERM)
                process.wait(timeout=20)
                if process.returncode!=0: raise ValueError('fixture shutdown failed')
            process=None
        def call(base, method, route, body=None, **headers):
            headers={'Authorization':'Bearer '+TOKEN, **headers}
            if isinstance(body,dict):
                headers['Content-Type']='application/json'; body=json.dumps(body).encode()
            req=Request(base+route,body,headers,method=method)
            with urlopen(req,timeout=10) as res:
                raw=res.read()
                return json.loads(raw)['data'] if raw else None
        try:
            base=start(current,data)
            session=call(base,'POST','/api/v1/staging-upload-sessions',{'filename':'fixture','size_bytes':'6','expected_checksum':{'algorithm':'md5','value':hashlib.md5(b'abcdef').hexdigest()}})
            route='/api/v1/staging-upload-sessions/'+session['session_id']
            call(base,'PATCH',route+'/content',b'abc',**{'Content-Type':'application/octet-stream','Upload-Offset':'0'})
            try:
                backup.backup(data,temp/'live-backup')
                raise AssertionError('live backup accepted')
            except ValueError as e:
                assert 'locked' in str(e)
            assert not (temp/'live-backup').exists()
            passed('refuse_live_directory_lock')
            stop()
            # Include additional artifact bytes; no attempt to forge a public
            # artifact or bypass its encrypted database permission metadata.
            artifact=data/'artifacts'/'backup-fixture'; artifact.write_bytes(b'ARTIFACT_FIXTURE'); artifact.chmod(0o600)
            archived=temp/'backup'; restored=temp/'restored'
            # Keep a quiescent fixture connection open so committed WAL+SHM
            # files survive. Only the stopped service tool owns service.lock.
            journal=sqlite3.connect(data/'service.db')
            try:
                assert journal.execute('PRAGMA journal_mode=WAL').fetchone()==('wal',)
                journal.execute('PRAGMA user_version=7');journal.commit()
                assert (data/'service.db-wal').stat().st_size>0
                before=backup.entries(data)
                assert backup.backup(data,archived)==7
                assert backup.restore(archived,restored)==7
                assert backup.entries(restored)==before
            finally:
                journal.close()
            passed('copy_key_database_journals_artifacts_staging_and_identity')
            corrupt=temp/'corrupt';shutil.copytree(archived,corrupt)
            (corrupt/'master.key').write_bytes(b'x'*32)
            try:
                backup.restore(corrupt,temp/'bad-restore')
                raise AssertionError('corrupt archive accepted')
            except ValueError as e: assert 'mismatch' in str(e)
            assert not (temp/'bad-restore').exists()
            passed('reject_corruption_before_destination_creation')
            link=data/'forbidden-link';link.symlink_to(token)
            try:
                backup.backup(data,temp/'symlink-backup')
                raise AssertionError('symlink archive accepted')
            except ValueError as e: assert 'special file' in str(e)
            link.unlink()
            passed('reject_symlink_data_escape')
            try:
                backup.restore(archived,restored)
                raise AssertionError('existing restore target overwritten')
            except ValueError as e: assert 'must not exist' in str(e)
            passed('restore_never_overwrites_existing_directory')
            original_copy=backup.shutil.copyfileobj
            def disk_full(incoming,outgoing,length):
                if str(outgoing.name).startswith(str(temp/'disk-full')+'/'):
                    outgoing.write(b'partial')
                    raise OSError(28,'fixture disk full')
                return original_copy(incoming,outgoing,length)
            with patch.object(backup.shutil,'copyfileobj',side_effect=disk_full):
                try:
                    backup.restore(archived,temp/'disk-full')
                    raise AssertionError('disk-full restore accepted')
                except OSError: pass
            assert not (temp/'disk-full').exists()
            passed('disk_full_cleans_new_destination_and_preserves_source')
            base=start(current,restored)
            state=call(base,'GET',route)
            assert state['offset_bytes']=='3' and state['staged_file_id'] is None
            call(base,'PATCH',route+'/content',b'def',**{'Content-Type':'application/octet-stream','Upload-Offset':'3'})
            result=call(base,'POST',route+'/complete')
            again=call(base,'POST',route+'/complete')
            assert result['staged_file_id']==again['staged_file_id']
            assert (restored/'staging'/result['staged_file_id']).read_bytes()==b'abcdef'
            stop()
            passed('restored_receiving_offset_resume_hash_complete_idempotency')
            snapshot=backup.digest(restored/'service.db')
            refusal=subprocess.run([str(old),'server','--data-dir',str(restored),'--listen','127.0.0.1:0','--token-file',str(token)],cwd=ROOT,capture_output=True,timeout=15)
            assert refusal.returncode!=0 and b'database schema is newer than this server' in refusal.stderr
            assert backup.digest(restored/'service.db')==snapshot
            passed('actual_old_binary_refuses_v7_without_database_change')
            # Roll back by restoring a whole pre-upgrade snapshot, never by
            # editing user_version on an already upgraded database.
            v6=temp/'v6-data';base=start(old,v6)
            call(base,'PATCH','/api/v1/configuration',{'no_check':False},**{'If-Match':'"1"'})
            stop()
            pre=temp/'pre-upgrade';assert backup.backup(v6,pre)==6
            base=start(current,v6)
            config=call(base,'GET','/api/v1/configuration')
            assert config['revision']=='2' and config['client']['no_check'] is False
            stop();assert backup.database_schema(v6)==7
            rollback=temp/'rollback';assert backup.restore(pre,rollback)==6
            base=start(old,rollback)
            config=call(base,'GET','/api/v1/configuration')
            assert config['revision']=='2' and config['client']['no_check'] is False
            stop()
            passed('v6_backup_upgrade_then_restore_v6_and_run_old_binary')
        finally:
            if process is not None and process.poll() is None:
                process.kill(); process.wait(timeout=10)
    report={'schema_version':1,'passed':True,'runtime':platform.system().lower()+'/'+{'x86_64':'amd64','aarch64':'arm64'}.get(platform.machine(),platform.machine()),'verified_at':datetime.now(timezone.utc).isoformat(),'sqlite_schema':7,'old_binary_commit':OLD,'cases':rows,'external_upstream':'none; fixture only'}
    if args.report:
        args.report.write_text(json.dumps(report,indent=2)+'\n')


if __name__=='__main__':
    main()
