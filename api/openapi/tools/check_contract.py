#!/usr/bin/env python3
"""Offline HTTP API contract and source checks. Run from any directory; never initialize the CLI."""
import argparse
import base64
import collections
import copy
import json
from pathlib import Path
import re
import subprocess
import sys
from urllib.parse import parse_qs, unquote, urlsplit

import yaml
from jsonschema import Draft202012Validator, FormatChecker
from openapi_spec_validator import validate_spec

ROOT = Path(__file__).resolve().parents[3]
API = ROOT / 'api/openapi'
METHODS = {'get', 'post', 'patch', 'put', 'delete', 'head', 'options', 'trace'}


class UniqueLoader(yaml.SafeLoader):
    pass


def unique_mapping(loader, node, deep=False):
    result = {}
    for key_node, value_node in node.value:
        key = loader.construct_object(key_node, deep=deep)
        if key in result:
            raise ValueError(f'duplicate YAML key: {key}')
        result[key] = loader.construct_object(value_node, deep=deep)
    return result


UniqueLoader.add_constructor(yaml.resolver.BaseResolver.DEFAULT_MAPPING_TAG, unique_mapping)


def require(condition, message):
    if not condition:
        raise ValueError(message)


def cells(line):
    return [c.strip().replace('\\|', '|') for c in re.split(r'(?<!\\)\|', line)[1:-1]]


def inventory(observed, operation_ids, implemented_count=0, partial_count=0):
    functions, commands, flags, config, options = {}, {}, {}, {}, {}
    section, option_type = '', ''
    for number, line in enumerate((ROOT / 'docs/http-api-coverage.md').read_text().splitlines(), 1):
        if line.startswith('## '):
            section = line.split()[1]
        if line.startswith('### '):
            option_type = line[4:].strip()
        if not line.startswith('| '):
            continue
        c = cells(line)
        if section == '2' and len(c) == 4 and c[0] not in ['命令', '---']:
            commands[c[0]] = {'mapping': c[1], 'notes': c[2], 'coverage_line': number}
        if section == '3' and len(c) == 3 and c[1].startswith('-') and c[1] != '---':
            flags[c[0] + ' --' + c[1].lstrip('-')] = {'mapping': c[2], 'coverage_line': number}
        if section == '4' and len(c) == 3 and c[0] not in ['源码字段', '---']:
            config['PCSConfig.' + c[0]] = {'mapping': c[1], 'notes': c[2], 'coverage_line': number}
        if section == '5' and len(c) == 3 and c[1].startswith('`'):
            options[option_type + '.' + c[0]] = {'mapping': c[2], 'coverage_line': number}
        if section in ['6', '7', '8', '9'] and len(c) >= 4:
            match = re.search(r'\[([^\]]+\.go):[0-9]+\]', c[-1])
            if match:
                functions[(match[1], c[0])] = {'mapping': c[1], 'notes': '；'.join(c[2:-1]), 'coverage_line': number}
    entries = copy.deepcopy(observed['entries'])
    for entry in entries:
        kind, name = entry['kind'], entry['name']
        if kind == 'function':
            row = functions.get((entry['file'], name))
        elif kind.startswith('command'):
            row = commands.get(name)
        elif kind == 'flag':
            row = flags.get(name)
        elif kind == 'configuration_field':
            row = config.get(name)
        elif kind == 'option_field':
            row = options.get(name)
        else:
            row = {'mapping': '启动期日志设置；CLI verbose，不公开秘密'} if kind == 'global_flag' else None
        require(row is not None, f'unmapped source entry: {kind} {entry["file"]} {name}')
        entry['coverage'] = row
        mapping = row['mapping']
        ids = set(re.findall(r'[A-Z]{3}[0-9]{2}', mapping))
        for prefix, a, b in re.findall(r'([A-Z]{3})([0-9]{2}) 至 [A-Z]{3}([0-9]{2})', mapping):
            ids.update(prefix + f'{x:02d}' for x in range(int(a), int(b) + 1))
        # "JOB 系列" is a domain mapping, not a missing per-function endpoint.
        for prefix in re.findall(r'([A-Z]{3})(?: 系列| 体系|体系)', mapping):
            ids.update(x for x in operation_ids if x.startswith(prefix))
        require(ids <= operation_ids, f'unknown route IDs for {name}: {ids - operation_ids}')
        entry['route_ids'] = sorted(ids)
        if 'CLI 专属' in mapping or kind == 'global_flag':
            classification = 'cli_only'
        elif kind == 'option_field' and name == 'Runner.Output':
            classification = 'internal_adapter'
        elif kind == 'function':
            classification = 'internal_adapter' if '内部' in row.get('notes', '') or not ids else 'http_business'
        else:
            classification = 'http_equivalent' if kind in ['flag', 'option_field', 'configuration_field'] else 'http_business'
        if not ids and classification not in ['cli_only', 'internal_adapter']:
            require(kind == 'function' or name == 'update --y', f'mapping lacks route or explicit exclusion: {name}')
            classification = 'internal_adapter' if kind == 'function' else 'cli_only'
        entry['classification'] = classification
    # Catch stale coverage rows as well as additions missing from the matrix.
    actual = {(e['file'], e['name']) for e in entries if e['kind'] == 'function'}
    require(actual == set(functions), f'stale function rows: {set(functions) - actual}')
    require(set(commands) == {e['name'] for e in entries if e['kind'].startswith('command')}, 'stale command rows')
    require(set(flags) == {e['name'] for e in entries if e['kind'] == 'flag'}, 'stale flag rows')
    require(set(config) == {e['name'] for e in entries if e['kind'] == 'configuration_field'}, 'stale configuration rows')
    require(set(options) == {e['name'] for e in entries if e['kind'] == 'option_field'}, 'stale option rows')
    return {
        'schema_version': 1,
        'source_baseline': '1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73',
        'generated_by': 'go run api/openapi/tools/inventory.go + check_contract.py --write-inventory',
        'status': {'source_mapping': 'complete', 'http_implementation': 'partial' if implemented_count or partial_count else 'planned', 'implemented_operations': implemented_count, 'partial_operations': partial_count, 'offline_contract': 'checked_separately', 'upstream_verification': 'scoped_p2_report', 'upstream_evidence': 'docs/http-api-p2-upstream-verification.json', 'p3_offline_evidence': 'docs/http-api-p3-verification.md', 'p3_upstream_verification': 'scoped_p3_report', 'p3_upstream_evidence': 'docs/http-api-p3-upstream-verification.json', 'p4_offline_evidence': 'docs/http-api-p4-verification.md', 'p4_upstream_verification': 'scoped_p4_report', 'p4_upstream_evidence': 'docs/http-api-p4-upstream-verification.json', 'p5_offline_evidence': 'docs/http-api-p5-verification.md', 'p5_upstream_verification': 'scoped_p5_report', 'p5_upstream_evidence': 'docs/http-api-p5-upstream-verification.json'},
        'counts': dict(sorted(collections.Counter(e['kind'] for e in entries).items())),
        'files': observed['files'], 'entries': entries,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--live-responses', type=Path, nargs='+', help='validate actual responses exported by P1 and P2 HTTP tests')
    parser.add_argument('--write-inventory', action='store_true', help='regenerate only after every source item has a matrix mapping')
    args = parser.parse_args()
    spec = yaml.load((API / 'openapi.yaml').read_text(), Loader=UniqueLoader)
    require(spec['openapi'] == '3.1.1', 'expected OpenAPI 3.1.1')
    require(json.loads((API / 'openapi.json').read_text()) == spec, 'embedded JSON drift; run export_contract.py or go generate ./api/openapi')
    validate_spec(spec)
    schemas = spec['components']['schemas']

    def resolve(value):
        if not isinstance(value, dict) or '$ref' not in value:
            return value
        target = value['$ref']
        require(target.startswith('#/'), f'external ref not allowed in offline contract: {target}')
        result = spec
        for part in target[2:].split('/'):
            result = result[part.replace('~1', '/').replace('~0', '~')]
        return result

    def walk(node):
        if isinstance(node, dict):
            if '$ref' in node:
                resolve(node)
            for value in node.values():
                walk(value)
        elif isinstance(node, list):
            for value in node:
                walk(value)

    walk(spec)
    for schema in schemas.values():
        Draft202012Validator.check_schema(schema)
    checks = 0

    def valid(schema, value, label, expected=True):
        nonlocal checks
        validator = Draft202012Validator({**schema, 'components': spec['components']}, format_checker=FormatChecker())
        errors = list(validator.iter_errors(value))
        if expected and errors:
            error = errors[0]
            raise ValueError(f'{label}: {list(error.path)}: {error.message}')
        require(expected or errors, f'{label}: invalid fixture unexpectedly passes')
        checks += 1

    routes = {}
    operations = {}
    request_examples, success_examples = set(), set()
    for path, item in spec['paths'].items():
        for method, op in item.items():
            if method not in METHODS:
                continue
            oid = op['operationId']
            require(oid not in operations, f'duplicate operationId {oid}')
            operations[oid] = op
            routes[oid] = (method.upper(), path, op['x-phase'])
            require(op['x-capability']['id'] == oid, f'{oid}: capability mismatch')
            require(op['x-capability']['implementation'] in ['planned','partial','implemented'], f'{oid}: invalid implementation status')
            if op['x-capability']['implementation'] in ['partial','implemented']:
                require(op.get('x-offline-evidence'), f'{oid}: implementation needs offline evidence')
                for evidence in op['x-offline-evidence']:
                    require((ROOT/evidence).is_file(), f'{oid}: missing offline evidence file')
            require('x-authorization' in op and 'x-idempotency' in op, f'{oid}: missing permission/idempotency decision')
            security = op.get('security', spec['security'])
            require(bool(security) or oid in ['SYS01', 'SYS02'], f'{oid}: accidental anonymous access')
            require(not any('DownloadTicketAuth' in s for s in security) or oid in ['DWN03', 'DWN04'], f'{oid}: ticket authorizes unrelated route')
            parameters = [resolve(p) for p in op.get('parameters', [])]
            names = [(p['in'], p['name']) for p in parameters]
            require(len(names) == len(set(names)), f'{oid}: duplicate parameter')
            require({p['name'] for p in parameters if p['in'] == 'path'} == set(re.findall(r'\{([^}]+)\}', path)), f'{oid}: path parameter mismatch')
            for parameter in parameters:
                require(parameter['in'] != 'path' or parameter['required'], f'{oid}: optional path parameter')
                if 'example' in parameter:
                    valid(parameter['schema'], parameter['example'], f'{oid} parameter {parameter["name"]}')
            require(op['x-idempotency']['supported'] == (('header', 'Idempotency-Key') in names), f'{oid}: idempotency/header mismatch')
            for media in op.get('requestBody', {}).get('content', {}).values():
                if media['schema'].get('format') == 'binary':
                    continue
                require(media.get('examples'), f'{oid}: missing request example')
                for example in media.get('examples', {}).values():
                    valid(media['schema'], example['value'], oid + ' request example')
                request_examples.add(oid)
            success = {status for status in op['responses'] if status.startswith('2')}
            require(success or oid == 'INT06', f'{oid}: missing success response')
            if op.get('x-job-kind'):
                require(success == {'202'}, f'{oid}: jobs must return 202')
                require(op['x-job-kind'] in schemas['JobKind']['enum'], f'{oid}: unknown job kind')
            if oid == 'INT06':
                require(not success, 'legacy rapid upload must not promise success')
            for status, response in op['responses'].items():
                response = resolve(response)
                require(not response.get('content') or method != 'head' and status != '204', f'{oid}: HEAD/204 has body')
                for media_type, media in response.get('content', {}).items():
                    if media_type == 'application/json' and oid != 'SYS08':
                        require(media.get('examples'), f'{oid}: missing JSON response example')
                    for example in media.get('examples', {}).values():
                        valid(media['schema'], example['value'], f'{oid} response {status}')
                    if status.startswith('2') and media_type == 'application/json' and oid != 'SYS08':
                        success_examples.add(oid)
            if oid not in ['SYS01', 'SYS02']:
                require({'401', '403', '501'} <= set(op['responses']), f'{oid}: missing identity/capability errors')
            if ('header', 'If-Match') in names:
                require({'412', '428'} <= set(op['responses']), f'{oid}: missing revision errors')
    expected_routes = {}
    for line in (ROOT / 'docs/http-api-design.md').read_text().splitlines():
        if re.match(r'^\| [A-Z]{3}[0-9]{2} \| (GET|POST|PATCH|HEAD|DELETE) ', line):
            oid, verb_path, _, phase = cells(line)
            method, path = verb_path.split(' ', 1)
            if path.startswith('A'):
                path = path.replace('A', '/api/v1/accounts/{account_id}', 1)
            elif path.startswith('B'):
                path = path.replace('B', '/api/v1', 1)
            require(oid not in expected_routes, f'duplicate route table ID {oid}')
            expected_routes[oid] = (method, path, phase)
    require(routes == expected_routes, f'design/OpenAPI route mismatch: {set(routes.items()) ^ set(expected_routes.items())}')
    cases = json.loads((API / 'examples/schema-cases.json').read_text())
    seen_cases = set()
    for case in cases:
        require(case['id'] not in seen_cases, f'duplicate case {case["id"]}')
        seen_cases.add(case['id'])
        require(case['schema'] in schemas, f'{case["id"]}: unknown schema')
        valid({'$ref': '#/components/schemas/' + case['schema']}, case['value'], case['id'], case['valid'])
    scenarios = json.loads((API / 'examples/behavior-cases.json').read_text())
    require(len({case['id'] for case in scenarios}) == len(scenarios), 'duplicate behavior case ID')
    for case in scenarios:
        for oid in case['operations']:
            require(oid in operations, f'{case["id"]}: unknown operation {oid}')
        require(case['verification'] in ['acceptance_spec_only', 'offline_tests'], f'{case["id"]}: invalid evidence state')
        if case['verification'] == 'offline_tests':
            require(case.get('offline_evidence'), f'{case["id"]}: missing runtime evidence')
            for evidence in case['offline_evidence']:
                filename, function = evidence.split('#', 1)
                require(filename.endswith('_test.go') and (ROOT/filename).is_file(), f'{case["id"]}: missing test file')
                require(re.search(r'func\s+' + re.escape(function) + r'\s*\(', (ROOT/filename).read_text()), f'{case["id"]}: missing test function')
    # Validate actual SSE wire example payloads, not merely the string media type.
    stream = (API / 'examples/job-events.sse').read_text()
    sse_events = 0
    for frame in stream.strip().split('\n\n'):
        fields = dict(line.split(': ', 1) for line in frame.splitlines() if ': ' in line and not line.startswith(':'))
        if 'data' not in fields:
            continue
        payload = json.loads(fields['data'])
        valid({'$ref': '#/components/schemas/JobEvent'}, payload, 'SSE ' + fields['event'])
        require(payload['event_id'] == fields['id'] and payload['type'] == fields['event'], 'SSE frame/payload mismatch')
        sse_events += 1
    require(sse_events >= 2, 'missing SSE data fixtures')
    # Every operation has a complete request/response pair, including headers and streams.
    wire = json.loads((API / 'examples/http-exchanges.json').read_text())
    require({e['operation_id'] for e in wire} == set(operations), 'missing HTTP exchanges')
    require(len(wire) == len(operations), 'duplicate HTTP exchange')
    for exchange in wire:
        oid = exchange['operation_id']; op = operations[oid]
        request = exchange['request']
        require(request['method'] == routes[oid][0], f'{oid}: wire method mismatch')
        url = urlsplit(request['url'])
        template = routes[oid][1]
        path_pattern = re.sub(r'\{[^}]+\}', r'([^/]+)', template)
        match = re.fullmatch(path_pattern, url.path)
        require(match is not None, f'{oid}: wire path mismatch')
        path_values = dict(zip(re.findall(r'\{([^}]+)\}', template), match.groups()))
        query = parse_qs(url.query, keep_blank_values=True)
        headers = {key.lower(): value for key, value in request['headers'].items()}
        for parameter in [resolve(p) for p in op.get('parameters', [])]:
            name, location = parameter['name'], parameter['in']
            values = path_values.get(name) if location == 'path' else query.get(name, [None])[0] if location == 'query' else headers.get(name.lower())
            require(values is not None or not parameter['required'], f'{oid}: missing wire {name}')
            if values is None:
                continue
            schema = resolve(parameter['schema'])
            if location == 'path':
                values = unquote(values)
            if schema.get('type') == 'integer':
                values = int(values)
            elif schema.get('type') == 'boolean':
                require(values in ['true', 'false'], f'{oid}: invalid boolean wire {name}')
                values = values == 'true'
            elif schema.get('type') == 'array':
                values = values.split(',')
            valid(parameter['schema'], values, f'{oid} wire {name}')
        if 'body' in request:
            media_type = request['headers']['Content-Type'].split(';', 1)[0]
            media = op['requestBody']['content'][media_type]
            valid(media['schema'], request['body'], f'{oid} wire request')
        status = str(exchange['response']['status'])
        require(status in op['responses'], f'{oid}: unknown wire status {status}')
        response = resolve(op['responses'][status])
        if oid == 'UPL07':
            require(exchange['response']['headers']['Upload-Offset'] == exchange['response']['body']['data']['offset_bytes'], 'UPL07: offset header/body mismatch')
        if request['method'] == 'HEAD' or status == '204':
            require(not any(key.startswith('body') for key in exchange['response']), f'{oid}: HEAD/204 wire body')
        if 'body_base64' in exchange['response']:
            payload = base64.b64decode(exchange['response']['body_base64'], validate=True)
            length = exchange['response']['headers'].get('Content-Length')
            require(length is None or len(payload) == int(length), f'{oid}: incorrect Content-Length')
        if 'body' in exchange['response'] and exchange['response']['content_type'] == 'application/json':
            valid(response['content']['application/json']['schema'], exchange['response']['body'], f'{oid} wire response')
            body = exchange['response']['body']
            require(body['meta']['request_id'] == exchange['response']['headers']['X-Request-ID'], f'{oid}: request ID mismatch')
            data = body['data']
            location = exchange['response']['headers'].get('Location')
            if isinstance(data, dict) and data.get('job_id') and location:
                require(location == '/api/v1/jobs/' + data['job_id'], f'{oid}: wrong job Location')
            if oid == 'DWN05':
                require(location == data['url'], 'DWN05: wrong ticket Location')
            etag = exchange['response']['headers'].get('ETag')
            if etag and isinstance(data, dict):
                revision = data.get('revision', data.get('configuration', {}).get('revision'))
                require(revision is None or etag == '"' + revision + '"', f'{oid}: ETag/revision mismatch')
    if args.live_responses:
        live = [record for filename in args.live_responses for record in json.loads(filename.read_text())]
        covered = set()
        rejected = set()
        for record in live:
            oid = record['operation_id']
            require(oid in operations, f'unknown live operation {oid}')
            response = resolve(operations[oid]['responses'][str(record['status'])])
            media_type = record['content_type']
            if record['method'] == 'HEAD' or record['status'] == 204:
                require(record['body'] is None, f'{oid}: HEAD/204 must have no body')
            else:
                require(media_type in response.get('content', {}), f'{oid}: unexpected live content type {media_type}')
                valid(response['content'][media_type]['schema'],record['body'],f'{oid} live {record["status"]}')
            if media_type == 'text/event-stream':
                require(isinstance(record['body'], str), f'{oid}: live SSE must be text')
                for frame in record['body'].split('\n\n'):
                    if not frame.strip(): continue
                    if frame.startswith(':'):
                        require('id:' not in frame, f'{oid}: heartbeat must have no ID')
                        continue
                    fields = dict(line.split(': ', 1) for line in frame.splitlines() if ': ' in line)
                    require(set(fields) == {'id', 'event', 'data'}, f'{oid}: malformed SSE frame')
                    event = json.loads(fields['data'])
                    valid({'$ref': '#/components/schemas/JobEvent'}, event, f'{oid} live SSE event')
                    require(fields['id'] == event['event_id'] and fields['event'] == event['type'], f'{oid}: SSE envelope drift')
            headers={key.lower():value[0] for key,value in record['headers'].items()}
            if media_type == 'application/json' and oid != 'SYS08':
                require(record['body']['meta']['request_id']==headers['x-request-id'],f'{oid}: live request ID mismatch')
            if media_type == 'application/problem+json' and record['method'] != 'HEAD':
                require(record['body']['status']==record['status'],f'{oid}: live Problem status mismatch')
            if record['status'] < 300: covered.add(oid)
            if record['status'] >= 400: rejected.add(oid)
            if oid == 'SYS08': require(record['body']==spec,'served OpenAPI differs from canonical YAML')
            if oid == 'SYS04' and record['status']==200:
                catalog=record['body']['data']
                require(len(catalog)==len(operations), 'live capability catalog count mismatch')
                by_id={entry['id']:entry for entry in catalog}
                require(set(by_id)==set(operations), 'live capability operation mismatch')
                for operation_id,entry in by_id.items():
                    state=operations[operation_id]['x-capability']['implementation']
                    require(entry['implementation']==state, f'{operation_id}: live capability state drift')
                    require(entry['enabled']==(state!='planned'), f'{operation_id}: live route enablement drift')
                    if state=='partial':
                        require(entry['availability']=='unverified' and entry['verified_at'] is None, f'{operation_id}: partial must not imply global upstream verification')
                        fixed=operations[operation_id].get('x-p2-limits',{}).get('fixed_options',{})
                        for name,value in fixed.items():
                            canonical=str(value).lower() if isinstance(value,bool) else str(value)
                            require(entry.get('supported_option_values',{}).get(name)==[canonical], f'{operation_id}: missing fixed option value {name}')
        implemented={oid for oid,op in operations.items() if op['x-capability']['implementation']=='implemented'}
        partial={oid for oid,op in operations.items() if op['x-capability']['implementation']=='partial'}
        require(covered >= implemented|partial,f'missing callable live operations: {(implemented|partial)-covered}')
        require(rejected >= partial,f'missing partial rejection coverage: {partial-rejected}')
        print(f'PASS: {len(live)} actual HTTP responses; {len(implemented)} implemented and {len(partial)} partial operations match OpenAPI')
    observed = json.loads(subprocess.check_output(['go', 'run', str(API / 'tools/inventory.go')], cwd=ROOT, text=True))
    implemented_count=sum(op['x-capability']['implementation']=='implemented' for op in operations.values())
    partial_count=sum(op['x-capability']['implementation']=='partial' for op in operations.values())
    baseline = inventory(observed, set(operations),implemented_count,partial_count)
    baseline_file = API / 'source-inventory.json'
    if args.write_inventory:
        baseline_file.write_text(json.dumps(baseline, ensure_ascii=False, indent=2) + '\n')
    else:
        require(json.loads(baseline_file.read_text()) == baseline, 'source or coverage matrix drift; review changes then run --write-inventory')
    print(f'PASS: OpenAPI 3.1.1; {len(routes)} operations / {len(spec["paths"])} paths / {len(schemas)} schemas')
    print(f'PASS: {len(request_examples)} typed request examples; {len(success_examples)} JSON response examples; {len(wire)} HTTP exchanges; {checks} instance validations')
    backed = sum(case['verification'] == 'offline_tests' for case in scenarios)
    print(f'PASS: {len(cases)} schema boundary cases; {len(scenarios)} behavior specifications ({backed} linked to offline tests, others pending); {sse_events} SSE frames')
    print(f'PASS: {len(observed["entries"])} AST entries mapped, no source/matrix drift; counts={baseline["counts"]}')


if __name__ == '__main__':
    try:
        main()
    except (ValueError, KeyError, AssertionError, subprocess.CalledProcessError) as error:
        print(f'FAIL: {error}', file=sys.stderr)
        sys.exit(1)
