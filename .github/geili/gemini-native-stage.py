#!/usr/bin/env python3
"""Candidate-bound HTTP acceptance for the shared Gemini native OpenAI bridge.

Only namespace-local synthetic upstreams are used. Canonical Stage/production
identity guards, exclusive lease, same-image replica and exact-ID restoration
come from the committed supplier runner. Defaults to read-only dry-run.
"""
import argparse
import copy
import fcntl
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import signal
import socket
from urllib.parse import urlsplit

spec = importlib.util.spec_from_file_location('gemini_native_supplier_base', Path(__file__).with_name('gemini-supplier-stage.py'))
supplier = importlib.util.module_from_spec(spec)
spec.loader.exec_module(supplier)

KIND = 'gemini-native-stage-v2'
CONTINUATION = 'Continue and complete the previous assistant turn from where it stopped. Do not repeat content already provided; use the tool results already present.'
TEXT = 'native acceptance OK'
SIGNATURES = ('c3ludGhldGljLXNpZ25hdHVyZS1h', 'c3ludGhldGljLXNpZ25hdHVyZS1i')
NAMES = ('fixture_lookup', 'fixture_other')
PNG = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII='
SCHEMA = {'type': 'object', 'properties': {'value': {'type': 'string'}}, 'required': ['value'], 'additionalProperties': False}
PROTOCOLS = ('responses', 'chat/completions')
# The product caps API key creation per user per hour (default 60); rotate owners below it.
KEY_SLOT_SIZE = 40
THINKING_MODEL = 'gemini-3-flash-preview'


def upstream_model(mode):
    # A fixture mapping establishes capabilities; the opaque argon alias does not.
    return THINKING_MODEL if mode == 'thinking' else supplier.MODEL


def base_payload(protocol, stream):
    value = {'model': supplier.MODEL, 'stream': stream}
    if protocol == 'responses':
        value.update(input='Synthetic native acceptance', max_output_tokens=16)
    else:
        value.update(messages=[{'role': 'user', 'content': 'Synthetic native acceptance'}], max_tokens=16)
        if stream:
            value['stream_options'] = {'include_usage': True}
    return value


def tools(protocol):
    rows = [{'name': name, 'description': 'Synthetic local function', 'parameters': {
        'type': 'object', 'properties': {'value': {'type': 'integer'}}, 'required': ['value'], 'additionalProperties': False}}
        for name in NAMES]
    return [dict(row, type='function') if protocol == 'responses' else {'type': 'function', 'function': row} for row in rows]


def scenarios(protocol):
    common = ['rich', 'thinking', 'nullable', 'json_object', 'json_schema', 'budget_one', 'tools', 'max_tokens',
              'reject_zero', 'reject_negative', 'reject_schema', 'reject_format', 'reject_state',
              'reject_store', 'reject_encrypted', 'reject_input', 'empty', 'missing_finish', 'error', 'disconnect']
    common += ['history_assistant', 'history_developer', 'history_blank', 'history_tools',
               'reject_history_missing', 'reject_history_duplicate', 'reject_history_orphan',
               'reject_history_empty', 'reject_history_media']
    return common + (['stop_string', 'stop_array'] if protocol == 'chat/completions' else
                     ['history_summary', 'history_additional', 'reject_history_summary', 'history_legacy'])


def case_payload(protocol, mode, stream):
    value = base_payload(protocol, stream)
    if mode.startswith('history_') or mode.startswith('reject_history_'):
        history_case_payload(value, protocol, mode)
    elif mode == 'rich':
        if protocol == 'responses':
            value.update(instructions='Native system marker', input=[
                {'role': 'user', 'content': 'Native first turn'}, {'role': 'assistant', 'content': 'Native previous answer'},
                {'role': 'user', 'content': [{'type': 'input_text', 'text': 'Native image marker'},
                    {'type': 'input_image', 'image_url': 'data:image/png;base64,' + PNG}]}])
        else:
            value['messages'] = [{'role': 'system', 'content': 'Native system marker'},
                {'role': 'user', 'content': 'Native first turn'}, {'role': 'assistant', 'content': 'Native previous answer'},
                {'role': 'user', 'content': [{'type': 'text', 'text': 'Native image marker'},
                    {'type': 'image_url', 'image_url': {'url': 'data:image/png;base64,' + PNG}}]}]
    elif mode == 'nullable':
        value.update({key: None for key in ('max_tokens', 'max_completion_tokens', 'max_output_tokens',
            'tools', 'functions', 'reasoning_effort', 'temperature', 'top_p')})
    elif mode in ('json_object', 'json_schema', 'reject_schema', 'reject_format'):
        format_type = 'not_a_format' if mode == 'reject_format' else ('json_object' if mode == 'json_object' else 'json_schema')
        schema = {'$ref': '#/$defs/value'} if mode == 'reject_schema' else copy.deepcopy(SCHEMA)
        fmt = {'type': format_type}
        if format_type == 'json_schema':
            fmt.update(name='fixture_json', strict=True, schema=schema)
        if protocol == 'responses':
            value['text'] = {'format': fmt}
        else:
            value['response_format'] = {'type': format_type, 'json_schema': {k: v for k, v in fmt.items() if k != 'type'}} if format_type == 'json_schema' else fmt
    elif mode in ('budget_one', 'reject_zero', 'reject_negative'):
        amount = {'budget_one': 1, 'reject_zero': 0, 'reject_negative': -1}[mode]
        if protocol == 'responses':
            value['max_output_tokens'] = amount
        else:
            value.update(max_tokens=16, max_completion_tokens=amount)
    elif mode == 'tools':
        value.update(tools=tools(protocol), tool_choice='required')
    elif mode == 'thinking':
        if protocol == 'responses':
            value['reasoning'] = {'effort': 'high'}
        else:
            value['reasoning_effort'] = 'high'
    elif mode in ('stop_string', 'stop_array'):
        value['stop'] = 'STOP_A' if mode == 'stop_string' else ['STOP_A', 'STOP_B']
    elif mode == 'reject_state':
        value['previous_response_id'] = 'resp_synthetic_previous'
    elif mode == 'reject_store':
        value['store'] = True
    elif mode == 'reject_encrypted':
        value['include'] = ['reasoning.encrypted_content']
    elif mode == 'reject_input':
        if protocol == 'responses':
            value['input'] = [{'type': 'input_audio', 'input_audio': {'data': 'synthetic', 'format': 'wav'}}]
        else:
            value['messages'] = [{'role': 'user', 'content': [{'type': 'input_audio', 'input_audio': {'data': 'synthetic', 'format': 'wav'}}]}]
    return value


def history_case_payload(value, protocol, mode):
    history = [{'role': 'user', 'content': 'Native first turn'},
               {'role': 'assistant', 'content': 'Native previous answer'}]
    if mode == 'history_developer':
        history.append({'role': 'developer', 'content': 'Native developer marker'})
    elif mode == 'history_blank':
        history.append({'role': 'user', 'content': [{'type': 'input_text' if protocol == 'responses' else 'text', 'text': '  '}]})
    elif mode == 'history_additional':
        history.append({'type': 'additional_tools', 'tools': tools('responses')})
    elif mode == 'reject_history_empty':
        history = [{'role': 'user', 'content': []}]
    elif mode == 'reject_history_media':
        part = ({'type': 'input_image', 'image_url': 'data:image/png;base64,' + PNG} if protocol == 'responses'
                else {'type': 'image_url', 'image_url': {'url': 'data:image/png;base64,' + PNG}})
        history[-1]['content'] = [part]
    elif mode in ('history_tools', 'history_summary', 'reject_history_missing', 'reject_history_duplicate', 'reject_history_orphan'):
        value['tools'] = tools(protocol)
        calls = [{'type': 'function_call', 'call_id': ident, 'name': NAMES[0], 'arguments': json.dumps({'value': i}),
                  'gemini_thought_signature': SIGNATURES[i]} for i, ident in enumerate(('native-a', 'native-b'))]
        results = [{'type': 'function_call_output', 'call_id': ident, 'output': '' if ident == 'native-a' else 'second'}
                   for ident in ('native-b', 'native-a')]
        if mode == 'reject_history_missing':
            results.pop()
        elif mode == 'reject_history_duplicate':
            results.append(copy.deepcopy(results[-1]))
        elif mode == 'reject_history_orphan':
            calls = []
        if protocol == 'responses':
            history = history[:1] + calls + results
        else:
            history = history[:1] + ([{'role': 'assistant', 'content': '', 'tool_calls': [
                {'id': c['call_id'], 'type': 'function', 'function': {'name': c['name'], 'arguments': c['arguments']},
                 'gemini_thought_signature': c['gemini_thought_signature']} for c in calls]}] if calls else []) + [
                {'role': 'tool', 'tool_call_id': r['call_id'], 'content': r['output']} for r in results]
        if mode == 'history_summary':
            history.append({'type': 'reasoning', 'summary': [{'type': 'summary_text', 'text': 'Historical display summary'}]})
    elif mode == 'reject_history_summary':
        history = [{'type': 'reasoning', 'summary': [{'type': 'summary_text', 'text': 'Historical display summary'}]}]
    if protocol == 'responses' and mode != 'history_legacy':
        value['input'] = history
    else:
        value.pop('input', None)
        value['messages'] = history


def native_history_error(body):
    contents = body.get('contents')
    if not isinstance(contents, list) or not contents or contents[-1].get('role') != 'user':
        return 'Requests ending with a model turn are not supported.'
    calls, consumed = {}, set()
    for turn in contents:
        parts = turn.get('parts')
        if turn.get('role') not in ('user', 'model') or not isinstance(parts, list) or not parts:
            return 'Empty or invalid native turn'
        for part in parts:
            if 'text' in part and (not isinstance(part['text'], str) or not part['text'].strip()):
                return 'Empty native text'
            if 'functionCall' in part:
                call = part['functionCall']
                ident = call.get('id') or ('anonymous-' + str(len(calls)))
                if turn['role'] != 'model' or ident in calls:
                    return 'Invalid or duplicate native function call'
                calls[ident] = call.get('name')
            if 'functionResponse' in part:
                result = part['functionResponse']
                ident = result.get('id')
                if not ident:
                    ident = next((k for k, name in calls.items() if k not in consumed and name == result.get('name')), None)
                if turn['role'] != 'user' or ident not in calls or ident in consumed or calls[ident] != result.get('name'):
                    return 'Orphan or duplicate native function result'
                consumed.add(ident)
    return None if len(calls) == len(consumed) else 'Missing native function result'


def chunk(parts=None, finish=None, usage=False):
    candidate = {'index': 0, 'content': {'role': 'model', 'parts': parts or []}}
    if finish is not None:
        candidate['finishReason'] = finish
    value = {'candidates': [candidate], 'modelVersion': supplier.MODEL}
    if usage:
        value['usageMetadata'] = {'promptTokenCount': 10, 'candidatesTokenCount': 2, 'totalTokenCount': 12}
    return value


def mock_rows(mode, stream):
    if mode == 'error':
        return [{'error': {'code': 503, 'message': 'Synthetic native error envelope', 'status': 'UNAVAILABLE'}}]
    if mode == 'empty':
        return [chunk(finish='STOP', usage=True)]
    if mode == 'thinking':
        return [chunk([{'text': 'Synthetic reasoning', 'thought': True}, {'text': TEXT}], finish='STOP', usage=True)]
    if mode == 'tools':
        if stream:
            return [chunk([{'functionCall': {'id': 'native-a', 'name': NAMES[0], 'args': '{"value":'}},
                           {'functionCall': {'id': 'native-b', 'name': NAMES[1], 'args': '{"value":'}}]),
                    chunk([{'functionCall': {'id': 'native-a', 'name': NAMES[0], 'args': '{"value":1}'}},
                           {'functionCall': {'id': 'native-b', 'name': NAMES[1], 'args': '{"value":2}'}}]),
                    chunk([{'thoughtSignature': SIGNATURES[0]}, {'thoughtSignature': SIGNATURES[1]}], finish='STOP', usage=True)]
        return [chunk([{'functionCall': {'id': 'native-a', 'name': NAMES[0], 'args': {'value': 1}}, 'thoughtSignature': SIGNATURES[0]},
                       {'functionCall': {'id': 'native-b', 'name': NAMES[1], 'args': {'value': 2}}, 'thoughtSignature': SIGNATURES[1]}], finish='STOP', usage=True)]
    text = json.dumps({'value': TEXT}) if mode in ('json_object', 'json_schema') else TEXT
    finish = None if mode in ('missing_finish', 'disconnect') else ('MAX_TOKENS' if mode == 'max_tokens' else 'STOP')
    return [chunk([{'text': text}], finish=finish, usage=True)]


def final_response(protocol, response, stream):
    if not stream:
        return response[1] if isinstance(response[1], dict) else {}
    rows = supplier.events(response[1])
    if protocol == 'responses':
        if any(e.get('error') or e.get('type') in ('error', 'response.failed') for e in rows):
            return {}
        terminal = [e['response'] for e in rows if e.get('type') in ('response.completed', 'response.incomplete') and isinstance(e.get('response'), dict)]
        return terminal[0] if len(terminal) == 1 else {}
    if sum(e.get('type') == '[DONE]' for e in rows) != 1 or any(e.get('error') or e.get('type') == 'error' for e in rows):
        return {}
    message, calls, finish, usage = {'role': 'assistant', 'content': ''}, {}, None, None
    for e in rows:
        if e.get('usage'):
            usage = e['usage']
        for choice in e.get('choices', []):
            if choice.get('finish_reason'):
                finish = choice['finish_reason']
            delta = choice.get('delta', {})
            message['content'] += delta.get('content') or ''
            if delta.get('reasoning_content'):
                message['reasoning_content'] = message.get('reasoning_content', '') + delta['reasoning_content']
            for call in delta.get('tool_calls', []):
                target = calls.setdefault(call['index'], {'type': 'function', 'function': {'name': '', 'arguments': ''}})
                if call.get('id'):
                    target['id'] = call['id']
                if call.get('gemini_thought_signature'):
                    target['gemini_thought_signature'] = call['gemini_thought_signature']
                function = call.get('function', {})
                target['function']['name'] += function.get('name') or ''
                target['function']['arguments'] += function.get('arguments') or ''
    if calls:
        message['tool_calls'] = [calls[key] for key in sorted(calls)]
    return {'choices': [{'message': message, 'finish_reason': finish}], 'usage': usage}


def output_text(protocol, final):
    if protocol == 'chat/completions':
        return final.get('choices', [{}])[0].get('message', {}).get('content', '')
    return ''.join(p.get('text', '') for item in final.get('output', []) for p in item.get('content', []) if p.get('type') == 'output_text')


def failed(protocol, response, stream):
    if response[0] >= 400:
        return True
    if not stream:
        return isinstance(response[1], dict) and bool(response[1].get('error'))
    rows = supplier.events(response[1])
    terminal = ('response.completed', 'response.incomplete') if protocol == 'responses' else ('[DONE]',)
    return any(e.get('error') or e.get('type') in ('response.failed', 'error') for e in rows) and not any(e.get('type') in terminal for e in rows)


def replay_payload(protocol, original, final):
    value = copy.deepcopy(original)
    value['tool_choice'] = 'auto'
    if protocol == 'responses':
        calls = [item for item in final.get('output', []) if item.get('type') == 'function_call']
        value['input'] = [{'role': 'user', 'content': 'Synthetic native acceptance'}] + calls + [
            {'type': 'function_call_output', 'call_id': item['call_id'], 'output': 'result-' + item['name']} for item in calls]
    else:
        message = final['choices'][0]['message']
        value['messages'].append(message)
        value['messages'].extend({'role': 'tool', 'tool_call_id': item['id'], 'content': 'result-' + item['function']['name']}
                                 for item in message.get('tool_calls', []))
    return value


def fixture_type(cache, source):
    class NativeFixture(supplier.fixture_type(cache, source)):
        def prepare(self):
            super().prepare()
            self.snapshot['gemini_native_runner'] = KIND
            cache.b.stage.private_json(self.private / 'snapshot.json', self.snapshot)

        def start_mock(self):
            super().start_mock()
            fixture, original = self, self.server.RequestHandlerClass
            self.native_calls, self.native_modes = [], {}
            class NativeMock(original):
                def do_POST(self):
                    match = supplier.route(self.path)
                    if not match or match.group(1) not in fixture.native_modes:
                        return self.reply({'error': {'message': 'unknown owned native route'}}, 404)
                    size = int(self.headers.get('Content-Length', '0'))
                    if not 0 < size <= 1048576:
                        return self.reply({'error': {'message': 'invalid synthetic body size'}}, 400)
                    body = json.loads(self.rfile.read(size))
                    label, endpoint = match.groups()
                    mode = fixture.native_modes[label]
                    with fixture.lock:
                        fixture.native_calls.append({'label': label, 'mode': mode, 'path': urlsplit(self.path).path, 'body': body})
                        if getattr(fixture, 'private', None):
                            cache.b.stage.private_json(fixture.private / 'gemini-native-mock-calls.json', fixture.native_calls)
                    if re.fullmatch(r'v1beta/models/' + re.escape(upstream_model(mode)) + r':(?:streamGenerateContent|generateContent)', endpoint) is None:
                        return self.reply({'error': {'message': 'Anthropic bridge is forbidden in native acceptance'}}, 400)
                    history_error = native_history_error(body)
                    if history_error:
                        return self.reply({'error': {'message': history_error}}, 400)
                    stream = ':streamGenerateContent' in endpoint
                    rows = mock_rows(mode, stream)
                    if not stream:
                        return self.reply(rows[0])
                    raw = ''.join('data: ' + json.dumps(row) + '\n\n' for row in rows).encode()
                    self.send_response(200)
                    self.send_header('Content-Type', 'text/event-stream')
                    self.send_header('Content-Length', str(len(raw) + (64 if mode == 'disconnect' else 0)))
                    self.end_headers()
                    self.wfile.write(raw)
                    self.wfile.flush()
                    if mode == 'disconnect':
                        self.close_connection = True
                        self.connection.shutdown(socket.SHUT_RDWR)
                        self.connection.close()
            self.server.RequestHandlerClass = NativeMock

        def key_owner(self):
            # One key per scenario exceeds api_key_create.max_per_user_per_hour (default
            # 60). Rotate owned synthetic users instead of relaxing the candidate limit.
            if not hasattr(self, '_key_owners'):
                self._key_owners = [self.owner]
            slot = len(self.native_modes) // KEY_SLOT_SIZE
            while len(self._key_owners) <= slot:
                owner = self.user('native-keys-' + str(len(self._key_owners)))
                # Same mock-only funding as the primary owner; balance keys are rejected at 0.
                self.api('/admin/users/' + str(owner['id']) + '/balance',
                         {'balance': 100, 'operation': 'set', 'notes': 'Owned mock-only Gemini acceptance; no payment'}, self.admin)
                self._key_owners.append(owner)
            return self._key_owners[slot]['token']

        def new_native_case(self, label, mode):
            group = self.group(label)
            self.native_modes[label] = mode
            account = self.api('/admin/accounts', {'name': 'Gemini native ' + label + ' ' + self.suffix,
                'platform': 'gemini', 'type': 'apikey', 'credentials': {'api_key': 'synthetic-native-only',
                'base_url': supplier.mock_url(self.mock_base, label), 'model_mapping': {supplier.MODEL: upstream_model(mode)}},
                'group_ids': [group['id']], 'priority': 1, 'concurrency': 4}, self.admin)
            self.snapshot['gemini_supplier_accounts'][str(account['id'])] = account['name']
            cache.b.stage.private_json(self.private / 'snapshot.json', self.snapshot)
            return self.api('/keys', {'name': 'Gemini native ' + label, 'billing_source': 'balance',
                'routing_mode': 'single', 'group_id': group['id'], 'quota': 1}, self.key_owner())

        def call(self, protocol, data, key, secondary):
            self.synthetic_requests += 1
            return (self.replica_request if secondary else self.request)('/v1/' + protocol, data, key['key'])

        def native_body_checks(self, label, mode, calls):
            self.check(label + ': exactly one native upstream call', len(calls) == 1, {'upstream_calls': len(calls)})
            call = calls[0]
            body = call['body']
            self.check(label + ': native model action without Anthropic fields', '/v1beta/models/' + upstream_model(mode) + ':' in call['path']
                and 'anthropic_version' not in body and 'messages' not in body and isinstance(body.get('contents'), list))
            cfg = body.get('generationConfig', {})
            self.check(label + ': valid native tail, nonempty turns and paired tools', native_history_error(body) is None)
            if mode in ('history_assistant', 'history_developer', 'history_blank', 'history_additional', 'history_legacy'):
                self.check(label + ': semantic continuation preserves assistant and appends one cue',
                    [c['role'] for c in body['contents']] == ['user', 'model', 'user']
                    and body['contents'][1]['parts'] == [{'text': 'Native previous answer'}]
                    and body['contents'][2]['parts'] == [{'text': CONTINUATION}])
                if mode == 'history_developer':
                    self.check(label + ': developer remains a system instruction',
                        body.get('systemInstruction', {}).get('parts') == [{'text': 'Native developer marker'}])
            if mode in ('history_tools', 'history_summary'):
                parts = [p for c in body['contents'] for p in c['parts']]
                calls = [p for p in parts if 'functionCall' in p]
                results = [p['functionResponse'] for p in parts if 'functionResponse' in p]
                self.check(label + ': reverse same-name tool results and signatures preserved',
                    [c['role'] for c in body['contents']] == ['user', 'model', 'user']
                    and [p.get('thoughtSignature') for p in calls] == list(SIGNATURES)
                    and [r.get('id') for r in results] == ['native-b', 'native-a']
                    and [r.get('response', {}).get('output') for r in results] == ['second', '']
                    and 'Historical display summary' not in json.dumps(body)
                    and CONTINUATION not in json.dumps(body))

            if mode == 'rich':
                contents = body['contents']
                self.check(label + ': instructions, roles and image preserved', body.get('systemInstruction', {}).get('parts') == [{'text': 'Native system marker'}]
                    and [c['role'] for c in contents] == ['user', 'model', 'user']
                    and contents[0]['parts'] == [{'text': 'Native first turn'}]
                    and contents[1]['parts'] == [{'text': 'Native previous answer'}]
                    and contents[2]['parts'] == [{'text': 'Native image marker'}, {'inlineData': {'mimeType': 'image/png', 'data': PNG}}])
            if mode in ('json_object', 'json_schema'):
                self.check(label + ': native JSON format', cfg.get('responseMimeType') == 'application/json')
                if mode == 'json_schema':
                    self.check(label + ': exact strict schema', cfg.get('responseJsonSchema') == SCHEMA)
            if mode == 'budget_one':
                self.check(label + ': explicit budget one is not clamped', cfg.get('maxOutputTokens') == 1)
            if mode == 'nullable':
                self.check(label + ': nullable defaults retained without native null parameters', cfg.get('maxOutputTokens') == 8192
                    and 'temperature' not in cfg and 'topP' not in cfg and 'tools' not in body)
            if mode == 'thinking':
                self.check(label + ': supported explicit thinking effort retained', cfg.get('thinkingConfig') == {'thinkingLevel': 'HIGH', 'includeThoughts': True})
            if mode.startswith('stop_'):
                self.check(label + ': native stopping strings', cfg.get('stopSequences') == (['STOP_A'] if mode == 'stop_string' else ['STOP_A', 'STOP_B']))
            if mode == 'tools':
                decls = [decl for tool in body.get('tools', []) for decl in tool.get('functionDeclarations', [])]
                self.check(label + ': native declarations and required selection', [d.get('name') for d in decls] == list(NAMES)
                    and all(d.get('parametersJsonSchema', {}).get('type') == 'object' for d in decls)
                    and body.get('toolConfig', {}).get('functionCallingConfig', {}).get('mode') == 'ANY')

        def ledger(self, label, key, expected=None):
            ident = str(int(key['id']))
            query = "SELECT json_build_object('logs',(SELECT COUNT(*) FROM usage_logs WHERE api_key_id=" + ident + "),'receipts',(SELECT COUNT(*) FROM usage_settlement_receipts WHERE api_key_id=" + ident + "),'settled',(SELECT COUNT(*) FROM usage_settlement_receipts WHERE api_key_id=" + ident + " AND state='settled' AND delivered_at IS NOT NULL))"
            if expected is not None:
                row = (self.eventually(label + ' accounting', lambda: (lambda r: r if r['settled'] >= expected else None)(self.sql(query)[0]))
                       if expected else self.sql(query)[0])
                self.check(label + ': no duplicate usage or settlement', row == {'logs': expected, 'receipts': expected, 'settled': expected}, row)
            else:
                row = self.sql(query)[0]
                self.check(label + ': failure cannot duplicate partial usage', row['logs'] <= 1 and row['receipts'] <= 1, row)
            if row['settled']:
                rows = self.sql("SELECT json_build_object('valid',r.charged_amount=u.actual_cost AND r.user_id=u.user_id AND r.api_key_id=u.api_key_id,'dedup',(SELECT COUNT(*) FROM usage_billing_dedup d WHERE d.request_id=r.request_id AND d.api_key_id=r.api_key_id AND d.request_fingerprint=r.request_fingerprint)) FROM usage_settlement_receipts r JOIN usage_logs u ON u.id=r.usage_log_id WHERE r.api_key_id=" + ident + ';')
                self.check(label + ': settled amounts and dedup agree', len(rows) == row['settled'] and all(r['valid'] is True and r['dedup'] == 1 for r in rows))

        def tool_replay(self, label, protocol, data, response, key, secondary):
            final = final_response(protocol, response, secondary)
            calls = ([r for r in final.get('output', []) if r.get('type') == 'function_call'] if protocol == 'responses'
                     else final.get('choices', [{}])[0].get('message', {}).get('tool_calls', []))
            self.check(label + ': two function calls with IDs and exact signatures', len(calls) == 2 and all(
                (call.get('call_id') if protocol == 'responses' else call.get('id')) and call.get('gemini_thought_signature') == SIGNATURES[i]
                and (call.get('name') if protocol == 'responses' else call.get('function', {}).get('name')) == NAMES[i]
                and json.loads(call.get('arguments') if protocol == 'responses' else call['function']['arguments']) == {'value': i + 1}
                for i, call in enumerate(calls)))
            replay = replay_payload(protocol, data, final)
            self.native_modes[label] = 'text'
            before = len(self.native_calls)
            answer = self.call(protocol, replay, key, secondary)
            actual = self.native_calls[before:]
            self.native_body_checks(label + ' replay', 'replay', actual)
            parts = [p for content in actual[0]['body']['contents'] for p in content['parts']]
            funcs = [p for p in parts if 'functionCall' in p]
            results = [p['functionResponse'] for p in parts if 'functionResponse' in p]
            self.check(label + ': tool result and signatures survive stateless replay', [p.get('thoughtSignature') for p in funcs] == list(SIGNATURES)
                and [r['name'] for r in results] == list(NAMES)
                and [r.get('response', {}).get('output') for r in results] == ['result-' + name for name in NAMES])
            self.check(label + ': replay completes with text', output_text(protocol, final_response(protocol, answer, secondary)) == TEXT)
            self.ledger(label, key, 2)

        def run_checks(self):
            self.synthetic_requests = 0
            self.scenario_results = []
            for secondary in (False, True):
                for protocol in PROTOCOLS:
                    for mode in scenarios(protocol):
                        # A disconnect exercises the streaming supplier read only.
                        if mode == 'disconnect' and not secondary:
                            continue
                        label = ('replica' if secondary else 'primary') + '-' + protocol.replace('/', '-') + '-' + mode.replace('_', '-')
                        key = self.new_native_case(label, mode)
                        data = case_payload(protocol, mode, secondary)
                        before = len(self.native_calls)
                        response = self.call(protocol, data, key, secondary)
                        actual = self.native_calls[before:]
                        if mode.startswith('reject_'):
                            self.check(label + ': explicit rejection before supplier', response[0] == 400 and not actual, {'status': response[0], 'supplier_calls': len(actual)})
                            self.ledger(label, key, 0)
                        else:
                            self.native_body_checks(label, mode, actual)
                            if mode in ('empty', 'missing_finish', 'error', 'disconnect'):
                                self.check(label + ': explicit failure without successful terminal', failed(protocol, response, secondary), {'status': response[0]})
                                self.ledger(label, key, 0 if mode in ('empty', 'error') else None)
                            elif mode == 'tools':
                                self.tool_replay(label, protocol, data, response, key, secondary)
                            else:
                                final = final_response(protocol, response, secondary)
                                if mode == 'max_tokens':
                                    self.check(label + ': budget truncation is incomplete', final.get('status') == 'incomplete' and final.get('incomplete_details', {}).get('reason') == 'max_output_tokens'
                                        if protocol == 'responses' else final.get('choices', [{}])[0].get('finish_reason') == 'length')
                                else:
                                    self.check(label + ': complete usable client answer', response[0] == 200 and
                                        (final.get('status') == 'completed' if protocol == 'responses' else final.get('choices', [{}])[0].get('finish_reason') == 'stop'))
                                text = output_text(protocol, final)
                                self.check(label + ': text and JSON content retained', json.loads(text) == {'value': TEXT} if mode in ('json_object', 'json_schema') else text == TEXT)
                                if mode == 'thinking':
                                    self.check(label + ': reasoning content preserved', 'Synthetic reasoning' in json.dumps(final))
                                usage = final.get('usage') or {}
                                self.check(label + ': exact client usage', usage.get('input_tokens' if protocol == 'responses' else 'prompt_tokens') == 10
                                    and usage.get('output_tokens' if protocol == 'responses' else 'completion_tokens') == 2 and usage.get('total_tokens') == 12)
                                self.ledger(label, key, 1)
                        self.scenario_results.append({'protocol': protocol, 'instance': 'replica' if secondary else 'primary', 'mode': mode, 'passed': True})
            self.check('protected Stage configuration and production unchanged', self.protected_state_unchanged())

        def restore(self):
            if self.snapshot and self.fixture_ids['accounts'] and self.snapshot.get('gemini_native_runner') != KIND:
                raise RuntimeError('restore snapshot belongs to another native runner')
            return super().restore()
    return NativeFixture


def main(argv=None):
    early = argparse.ArgumentParser(add_help=False)
    early.add_argument('--ops-bin', required=True)
    known, remaining = early.parse_known_args(argv)
    cache = supplier.load_ops(known.ops_bin)
    parser = cache.b.candidate_args()
    parser.add_argument('--replica-port', type=int, default=18519)
    args = parser.parse_args(remaining)
    supplier.validate_options(args)
    cache.b.validate_args(args)
    fixture = fixture_type(cache, cache.b.stage.load_source(args.source_root))(args)
    if args.dry_run:
        return 0 if fixture.execute_billing() else 1
    with (cache.b.stage.LAB / '.v2-synthetic.lock').open('a') as lock:
        os.chmod(lock.name, 0o600)
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
        passed = fixture.execute_billing()
    if fixture.snapshot:
        cases = getattr(fixture, 'scenario_results', [])
        expected = sum(len(scenarios(protocol)) for protocol in PROTOCOLS) * 2 - len(PROTOCOLS)
        # Only this runner's fixed synthetic bodies, never credentials or headers.
        calls = getattr(fixture, 'native_calls', [])
        evidence = fixture.private / 'gemini-native-mock-calls.json'
        if not args.restore:
            cache.b.stage.private_json(evidence, calls)
        result = {'kind': KIND, 'revision': args.expected_revision, 'image': args.expected_image,
            'runner_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
            'supplier_runner_sha256': hashlib.sha256(Path(supplier.__file__).read_bytes()).hexdigest(),
            'canonical_ops_sha256': hashlib.sha256(Path(cache.__file__).read_bytes()).hexdigest(),
            'checks': fixture.checks, 'scenarios': cases, 'expected_scenarios': expected,
            'synthetic_requests': getattr(fixture, 'synthetic_requests', 0),
            'supplier_calls': len(calls),
            'mock_evidence_sha256': hashlib.sha256(evidence.read_bytes()).hexdigest() if evidence.is_file() else None,
            'restored': fixture.snapshot.get('restored', False),
            'runtime_restored': fixture.snapshot.get('gemini_supplier_runtime_restored', False),
            'replica_removed': fixture.snapshot.get('cache_replica', {}).get('removed', False),
            'acceptance_executed': not bool(args.restore), 'synthetic_only': True,
            'payments_executed': False, 'real_suppliers_executed': False, 'production_ready': False,
            'external_unverified': ['real suppliers', 'payments', 'production deployment', 'performance gate']}
        result['matrix_complete'] = len(cases) == expected and not bool(args.restore)
        result['passed'] = passed and result['restored'] and result['runtime_restored'] and result['replica_removed'] and (bool(args.restore) or result['matrix_complete'])
        filename = 'gemini-native-restore-result.json' if args.restore else 'gemini-native-result.json'
        cache.b.stage.private_json(fixture.private / filename, result)
        print('RESULT ' + str(fixture.private / filename), flush=True)
        passed = result['passed']
    return 0 if passed else 1


if __name__ == '__main__':
    raise SystemExit(main())
