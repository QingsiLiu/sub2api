#!/usr/bin/env python3
"""Synthetic local HTTP audit acceptance. Never uses paid upstream credentials."""
import argparse
import importlib.util
import json
from pathlib import Path
import secrets
import time
import urllib.parse

spec = importlib.util.spec_from_file_location('geili_acceptance', Path(__file__).with_name('acceptance.py'))
h = importlib.util.module_from_spec(spec)
spec.loader.exec_module(h)

class AuditMock(h.Mock):
    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers.get('Content-Length', 0))))
        mode = self.path.split('/')[1]
        path = urllib.parse.urlsplit(self.path).path
        protocol = 'messages' if path.endswith('/messages') else 'responses' if path.endswith('/responses') else 'chat'
        model, rid = body.get('model'), 'synthetic-' + secrets.token_hex(8)
        usage = {'input_tokens': 1000, 'output_tokens': 0}
        content = []
        if mode in ('text', 'partial'):
            content = [{'type': 'text', 'text': 'synthetic usable output'}]
        elif mode == 'thinking':
            content = [{'type': 'thinking', 'thinking': 'synthetic readable thinking'}]
        elif mode == 'tool':
            content = [{'type': 'tool_use', 'id': 'call_1', 'name': 'lookup', 'input': {}}]
        if protocol == 'messages':
            result = {'id': rid, 'type': 'message', 'role': 'assistant', 'model': model, 'content': content, 'stop_reason': 'tool_use' if mode == 'tool' else 'end_turn', 'usage': usage}
        elif protocol == 'responses':
            output = []
            if content:
                if mode == 'tool':
                    output = [{'type': 'function_call', 'id': 'item_1', 'call_id': 'call_1', 'name': 'lookup', 'arguments': '{}', 'status': 'completed'}]
                else:
                    output = [{'type': 'message', 'id': 'msg_1', 'role': 'assistant', 'status': 'completed', 'content': [{'type': 'output_text', 'text': 'synthetic usable output', 'annotations': []}]}]
            if mode == 'unknown':
                output = [{'type': 'future_provider_output', 'data': 'opaque'}]
            result = {'id': rid, 'object': 'response', 'created_at': int(time.time()), 'status': 'completed', 'error': None, 'model': model, 'output': output, 'usage': {**usage, 'total_tokens': 1000}}
        else:
            message = {'role': 'assistant', 'content': 'synthetic usable output' if mode == 'text' else ''}
            if mode == 'tool':
                message['tool_calls'] = [{'id': 'call_1', 'type': 'function', 'function': {'name': 'lookup', 'arguments': '{}'}}]
            result = {'id': rid, 'object': 'chat.completion', 'model': model, 'created': int(time.time()), 'choices': [{'index': 0, 'message': message, 'finish_reason': 'tool_calls' if mode == 'tool' else 'stop'}], 'usage': {'prompt_tokens': 1000, 'completion_tokens': 0, 'total_tokens': 1000}}
        if not body.get('stream'):
            return self.reply(result)
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream')
        self.end_headers()
        def event(kind, value):
            data = (('event: ' + kind + '\n') if kind else '') + 'data: ' + json.dumps(value) + '\n\n'
            self.wfile.write(data.encode()); self.wfile.flush()
        if protocol == 'messages':
            event('message_start', {'type': 'message_start', 'message': {**result, 'content': [], 'stop_reason': None}})
            for index, block in enumerate(content):
                start_block = dict(block)
                field = 'text' if block['type'] == 'text' else 'thinking' if block['type'] == 'thinking' else None
                if field: start_block[field] = ''
                event('content_block_start', {'type': 'content_block_start', 'index': index, 'content_block': start_block})
                if field:
                    event('content_block_delta', {'type': 'content_block_delta', 'index': index, 'delta': {'type': field + '_delta', field: block[field]}})
                event('content_block_stop', {'type': 'content_block_stop', 'index': index})
            event('message_delta', {'type': 'message_delta', 'delta': {'stop_reason': result['stop_reason']}, 'usage': {'output_tokens': 0}})
            event('message_stop', {'type': 'message_stop'})
        elif protocol == 'responses':
            event('response.created', {'type': 'response.created', 'response': {**result, 'output': [], 'status': 'in_progress', 'usage': None}})
            if mode == 'tool':
                item = result['output'][0]
                event('response.output_item.added', {'type': 'response.output_item.added', 'output_index': 0, 'item': {**item, 'arguments': '', 'status': 'in_progress'}})
                event('response.function_call_arguments.delta', {'type': 'response.function_call_arguments.delta', 'output_index': 0, 'item_id': 'item_1', 'delta': '{}'})
                event('response.function_call_arguments.done', {'type': 'response.function_call_arguments.done', 'output_index': 0, 'item_id': 'item_1', 'arguments': '{}'})
                event('response.output_item.done', {'type': 'response.output_item.done', 'output_index': 0, 'item': item})
            if mode in ('text', 'partial'):
                event('response.output_text.delta', {'type': 'response.output_text.delta', 'item_id': 'msg_1', 'output_index': 0, 'content_index': 0, 'delta': 'synthetic usable output'})
            if mode in ('partial', 'failed'):
                event('response.failed', {'type': 'response.failed', 'response': {**result, 'output': [], 'status': 'failed', 'error': {'code': 'synthetic_error', 'message': 'synthetic upstream failure'}}})
            else:
                event('response.completed', {'type': 'response.completed', 'response': result})
        else:
            delta = dict(result['choices'][0]['message'])
            if mode == 'tool': delta['tool_calls'][0]['index'] = 0
            event('', {**result, 'object': 'chat.completion.chunk', 'choices': [{'index': 0, 'delta': delta, 'finish_reason': None}], 'usage': None})
            event('', {**result, 'object': 'chat.completion.chunk', 'choices': [{'index': 0, 'delta': {}, 'finish_reason': result['choices'][0]['finish_reason']}]})
            self.wfile.write(b'data: [DONE]\n\n'); self.wfile.flush()

h.Mock = AuditMock

def verify():
    cfg = h.local_env()
    admin = h.api('/auth/login', {'email': 'admin@acceptance.invalid', 'password': cfg['admin_password']})['access_token']
    stamp = str(time.time_ns())
    user = h.api('/admin/users', {'email': 'audit-' + stamp + '@example.invalid', 'password': cfg['admin_password'], 'balance': 5, 'concurrency': 5}, admin)
    token = h.api('/auth/login', {'email': user['email'], 'password': cfg['admin_password']})['access_token']
    cases = []
    for protocol, platform in [('messages', 'anthropic'), ('responses', 'openai'), ('chat/completions', 'openai')]:
        for stream in (False, True):
            for mode in ('text', 'empty', 'tool'):
                cases.append((protocol, platform, mode, stream, 'empty' if mode == 'empty' else 'success'))
    cases += [('messages', 'anthropic', 'thinking', True, 'success'), ('responses', 'openai', 'partial', True, 'partial_failure'), ('responses', 'openai', 'failed', True, 'failed'), ('responses', 'openai', 'unknown', True, 'unknown'), ('messages', 'openai', 'text', True, 'success'), ('chat/completions', 'anthropic', 'text', True, 'success')]
    fixtures = {}
    report = []
    for protocol, platform, mode, stream, expected in cases:
        pair = (platform, mode)
        model = 'claude-sonnet-4-6' if platform == 'anthropic' else 'gpt-4.1-mini'
        if pair not in fixtures:
            group = h.api('/admin/groups', {'name': 'audit-' + '-'.join(pair) + '-' + stamp, 'platform': platform, 'subscription_type': 'standard', 'allow_messages_dispatch': True, 'rate_multiplier': 1, 'model_pricing': [{'models': [model], 'billing_mode': 'token', 'input_price': .000001, 'output_price': .000002}]}, admin)
            h.api('/admin/accounts', {'name': 'audit-' + '-'.join(pair) + '-' + stamp, 'platform': platform, 'type': 'apikey', 'credentials': {'api_key': 'synthetic-test-key', 'base_url': f'http://127.0.0.1:{h.MOCK_PORT}/' + mode, 'model_mapping': {model: model}}, 'extra': {'openai_responses_mode': 'force_responses'} if platform == 'openai' else {}, 'group_ids': [group['id']], 'concurrency': 5, 'priority': 1}, admin)
            fixtures[pair] = h.api('/keys', {'name': 'audit-' + '-'.join(pair), 'billing_source': 'balance', 'routing_mode': 'single', 'group_id': group['id'], 'quota': 1}, token)
        key = fixtures[pair]
        def snapshot():
            return h.sql(f"SELECT json_build_object('balance',balance,'quota',(SELECT quota_used FROM api_keys WHERE id={key['id']})) FROM users WHERE id={user['id']};")[0]
        before = snapshot()
        payload = {'model': model, 'stream': stream, 'max_tokens': 16, 'messages': [{'role': 'user', 'content': 'synthetic test'}]}
        if protocol == 'responses': payload = {'model': model, 'stream': stream, 'max_output_tokens': 16, 'input': 'synthetic test'}
        status, _ = h.request('/v1/' + protocol, payload, key['key'], raw=True)
        assert status == 200 or (expected == 'failed' and status == 502), (protocol, mode, status)
        audit = None
        for _ in range(100):
            data = h.api('/admin/usage/response-audits?' + urllib.parse.urlencode({'user_id': user['id'], 'api_key_id': key['id'], 'page_size': 1}), token=admin)
            if data['items']:
                candidate = data['items'][0]
                if candidate['id'] not in [x['audit_id'] for x in report] and (candidate['settlement_state'] == 'settled' or expected in ('failed', 'partial_failure')):
                    audit = candidate; break
            time.sleep(.1)
        assert audit is not None, (protocol, mode, 'audit or receipt missing')
        assert audit['status'] == expected, (protocol, mode, stream, audit['status'], audit['reason'])
        after = snapshot()
        charge = float(audit['charged_amount']) if audit.get('charged_amount') is not None else 0
        assert abs(before['balance'] - after['balance'] - charge) < 1e-9
        assert abs(after['quota'] - before['quota'] - charge) < 1e-9
        if expected in ('success', 'empty', 'unknown'):
            assert charge > 0, 'zero output tokens must not change existing input billing'
        detail = h.api('/admin/usage/response-audits/' + str(audit['id']), token=admin)
        assert detail['audit_request_id'] == audit['audit_request_id']
        report.append({'protocol': protocol, 'platform': platform, 'mode': mode, 'stream': stream, 'status': expected, 'http_status': status, 'audit_id': audit['id'], 'existing_charge': charge, 'billing_unchanged': True})
        print('PASS', protocol, platform, mode, 'SSE' if stream else 'JSON', expected, flush=True)
    # Admin authorization and JSON data privacy, plus aggregate consistency.
    for who, expected in [(None, 401), (token, 403)]:
        status, _ = h.request('/api/v1/admin/usage/response-audits', token=who)
        assert status == expected, ('admin authorization', status)
    stats = h.api('/admin/usage/response-audits/stats?user_id=' + str(user['id']), token=admin)
    assert stats['total'] == len(report) == sum(stats['counts'].values())
    assert stats['write_failures_since_start'] == 0 and stats['dropped_since_start'] == 0
    rows = h.api('/admin/usage/response-audits?user_id=' + str(user['id']), token=admin)['items']
    assert 'synthetic usable output' not in json.dumps(rows) and 'synthetic readable thinking' not in json.dumps(rows)
    logs = h.api('/admin/usage?user_id=' + str(user['id']), token=admin)['items']
    assert all(x.get('response_audit') for x in logs)
    user_logs = h.api('/usage', token=token)['items']
    assert all('response_audit' not in x for x in user_logs)
    h.PRIVATE.joinpath('response-audit-report.json').write_text(json.dumps({'mode': 'isolated_synthetic', 'cases': report, 'stats': stats, 'admin_only': True, 'payload_not_persisted': True, 'user_usage_unchanged': True}, indent=2) + '\n')
    print('PASS admin-only APIs, unchanged user DTO, read-only billing association, privacy and stats', flush=True)

if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('command', choices=['serve', 'verify'])
    args = parser.parse_args()
    h.serve() if args.command == 'serve' else verify()
