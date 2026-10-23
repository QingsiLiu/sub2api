#!/usr/bin/env python3
"""Offline Stage runner tests; no candidate, payment or supplier requests."""
import copy
import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import importlib.util
import json
from pathlib import Path
import threading
from types import SimpleNamespace
import unittest

spec = importlib.util.spec_from_file_location('gemini_native_stage', Path(__file__).with_name('gemini-native-stage.py'))
s = importlib.util.module_from_spec(spec)
spec.loader.exec_module(s)


def sse(rows):
    return ''.join('data: ' + ('[DONE]' if r == '[DONE]' else json.dumps(r)) + '\n\n' for r in rows)


class OracleTests(unittest.TestCase):
    def test_input_matrix_contains_only_requested_protocols(self):
        self.assertEqual(s.PROTOCOLS, ('responses', 'chat/completions'))
        self.assertEqual(len(s.scenarios('responses')), 33)
        self.assertEqual(len(s.scenarios('chat/completions')), 31)
        self.assertEqual(sum(len(s.scenarios(p)) for p in s.PROTOCOLS) * 2 - 2, 126)

    def test_nullable_sdk_fields_are_sent_as_null_and_thinking_is_explicitly_mapped(self):
        for protocol in s.PROTOCOLS:
            body = s.case_payload(protocol, 'nullable', False)
            for key in ('max_tokens', 'max_completion_tokens', 'max_output_tokens', 'tools',
                        'functions', 'reasoning_effort', 'temperature', 'top_p'):
                self.assertIn(key, body)
                self.assertIsNone(body[key])
        self.assertEqual(s.upstream_model('thinking'), s.THINKING_MODEL)
        self.assertNotEqual(s.upstream_model('thinking'), s.supplier.MODEL)
        self.assertEqual(s.upstream_model('nullable'), s.supplier.MODEL)

    def test_budget_and_formats_use_the_real_client_wire_shapes(self):
        for protocol in s.PROTOCOLS:
            for mode, budget in [('budget_one', 1), ('reject_zero', 0), ('reject_negative', -1)]:
                body = s.case_payload(protocol, mode, False)
                self.assertEqual(body['max_output_tokens' if protocol == 'responses' else 'max_completion_tokens'], budget)
                if protocol == 'chat/completions':
                    self.assertEqual(body['max_tokens'], 16)
            body = s.case_payload(protocol, 'json_schema', True)
            fmt = body['text']['format'] if protocol == 'responses' else body['response_format']['json_schema']
            self.assertEqual(fmt['schema'], s.SCHEMA)
            self.assertIs(fmt['strict'], True)
            invalid = s.case_payload(protocol, 'reject_schema', False)
            fmt = invalid['text']['format'] if protocol == 'responses' else invalid['response_format']['json_schema']
            self.assertIn('$ref', fmt['schema'])

    def test_rich_inputs_contain_system_history_and_real_inline_png(self):
        response = s.case_payload('responses', 'rich', False)
        self.assertEqual(response['instructions'], 'Native system marker')
        self.assertEqual([r['role'] for r in response['input']], ['user', 'assistant', 'user'])
        self.assertEqual(response['input'][-1]['content'][-1]['image_url'], 'data:image/png;base64,' + s.PNG)
        chat = s.case_payload('chat/completions', 'rich', True)
        self.assertEqual([r['role'] for r in chat['messages']], ['system', 'user', 'assistant', 'user'])
        self.assertEqual(chat['messages'][-1]['content'][-1]['image_url']['url'], 'data:image/png;base64,' + s.PNG)

    def test_explicit_thinking_uses_each_client_protocol_field(self):
        self.assertEqual(s.case_payload('responses', 'thinking', True)['reasoning'], {'effort': 'high'})
        self.assertEqual(s.case_payload('chat/completions', 'thinking', True)['reasoning_effort'], 'high')
        parts = s.mock_rows('thinking', False)[0]['candidates'][0]['content']['parts']
        self.assertTrue(parts[0]['thought'])
        self.assertNotIn('thought', parts[1])

    def test_history_matrix_carries_real_tail_and_tool_pairing_boundaries(self):
        for protocol in s.PROTOCOLS:
            field = 'input' if protocol == 'responses' else 'messages'
            self.assertEqual(s.case_payload(protocol, 'history_assistant', False)[field][-1]['role'], 'assistant')
            self.assertEqual(s.case_payload(protocol, 'history_developer', True)[field][-1]['role'], 'developer')
            self.assertEqual(s.case_payload(protocol, 'history_blank', True)[field][-1]['content'][0]['text'], '  ')
            history = s.case_payload(protocol, 'history_tools', False)[field]
            outputs = [r for r in history if r.get('type') == 'function_call_output' or r.get('role') == 'tool']
            self.assertEqual([r.get('call_id', r.get('tool_call_id')) for r in outputs], ['native-b', 'native-a'])
            self.assertEqual(outputs[-1].get('output', outputs[-1].get('content')), '')
            missing = s.case_payload(protocol, 'reject_history_missing', False)[field]
            self.assertEqual(sum(r.get('type') == 'function_call_output' or r.get('role') == 'tool' for r in missing), 1)
        self.assertEqual(s.case_payload('responses', 'history_summary', False)['input'][-1]['type'], 'reasoning')
        self.assertEqual(s.case_payload('responses', 'history_additional', False)['input'][-1]['type'], 'additional_tools')
        legacy = s.case_payload('responses', 'history_legacy', False)
        self.assertNotIn('input', legacy)
        self.assertEqual(legacy['messages'][-1]['role'], 'assistant')

    def test_responses_terminal_selection_rejects_duplicate_successes(self):
        done = {'type': 'response.completed', 'response': {'status': 'completed', 'output': []}}
        self.assertEqual(s.final_response('responses', [200, sse([done])], True)['status'], 'completed')
        self.assertEqual(s.final_response('responses', [200, sse([done, done])], True), {})
        self.assertEqual(s.final_response('responses', [200, sse([{'type': 'response.created'}])], True), {})

    def test_chat_aggregates_fragmented_arguments_and_late_signature(self):
        rows = [
            {'choices': [{'delta': {'tool_calls': [{'index': 0, 'id': 'call_a', 'type': 'function', 'function': {'name': s.NAMES[0], 'arguments': '{"value":'}}]}, 'finish_reason': None}]},
            {'choices': [{'delta': {'tool_calls': [{'index': 0, 'function': {'arguments': '1}'}}]}, 'finish_reason': None}]},
            {'choices': [{'delta': {'tool_calls': [{'index': 0, 'gemini_thought_signature': s.SIGNATURES[0]}]}, 'finish_reason': None}]},
            {'choices': [{'delta': {}, 'finish_reason': 'tool_calls'}]},
            {'choices': [], 'usage': {'prompt_tokens': 10, 'completion_tokens': 2, 'total_tokens': 12}}, '[DONE]']
        final = s.final_response('chat/completions', [200, sse(rows)], True)
        call = final['choices'][0]['message']['tool_calls'][0]
        self.assertEqual(call['id'], 'call_a')
        self.assertEqual(call['function'], {'name': s.NAMES[0], 'arguments': '{"value":1}'})
        self.assertEqual(call['gemini_thought_signature'], s.SIGNATURES[0])
        self.assertEqual(final['usage']['total_tokens'], 12)
        self.assertEqual(s.final_response('chat/completions', [200, sse(rows[:-1])], True), {})
        self.assertEqual(s.final_response('chat/completions', [200, sse(rows + ['[DONE]'])], True), {})

    def test_stateless_replay_preserves_signatures_and_correlates_tool_outputs(self):
        response_calls = [{'type': 'function_call', 'call_id': 'call_' + str(i), 'name': name,
            'arguments': json.dumps({'value': i + 1}), 'gemini_thought_signature': s.SIGNATURES[i]} for i, name in enumerate(s.NAMES)]
        original = s.case_payload('responses', 'tools', False)
        before = copy.deepcopy(original)
        replay = s.replay_payload('responses', original, {'output': response_calls})
        self.assertEqual(original, before)
        self.assertEqual(replay['input'][1:3], response_calls)
        self.assertEqual([r['call_id'] for r in replay['input'][3:]], ['call_0', 'call_1'])
        self.assertEqual(replay['tool_choice'], 'auto')
        chat_calls = [{'id': r['call_id'], 'type': 'function', 'function': {'name': r['name'], 'arguments': r['arguments']},
            'gemini_thought_signature': r['gemini_thought_signature']} for r in response_calls]
        replay = s.replay_payload('chat/completions', s.case_payload('chat/completions', 'tools', True),
            {'choices': [{'message': {'role': 'assistant', 'content': '', 'tool_calls': chat_calls}}]})
        self.assertEqual(replay['messages'][1]['tool_calls'], chat_calls)
        self.assertEqual([r['tool_call_id'] for r in replay['messages'][2:]], ['call_0', 'call_1'])

    def test_failure_requires_explicit_error_and_no_successful_terminal(self):
        for protocol in s.PROTOCOLS:
            self.assertTrue(s.failed(protocol, [503, {'error': {'message': 'synthetic'}}], False))
            self.assertFalse(s.failed(protocol, [200, {}], False))
            self.assertFalse(s.failed(protocol, [200, ''], True))
            error = {'type': 'response.failed', 'error': {'message': 'synthetic'}}
            self.assertTrue(s.failed(protocol, [200, sse([error])], True))
            done = {'type': 'response.completed'} if protocol == 'responses' else '[DONE]'
            self.assertFalse(s.failed(protocol, [200, sse([error, done])], True))

    def test_native_tool_mock_contains_real_fragment_and_late_signature(self):
        rows = s.mock_rows('tools', True)
        self.assertEqual(rows[0]['candidates'][0]['content']['parts'][0]['functionCall']['args'], '{"value":')
        self.assertEqual(rows[1]['candidates'][0]['content']['parts'][0]['functionCall']['args'], '{"value":1}')
        self.assertEqual(rows[-1]['candidates'][0]['content']['parts'][0], {'thoughtSignature': s.SIGNATURES[0]})
        self.assertEqual(rows[-1]['candidates'][0]['finishReason'], 'STOP')


class MockHTTPTests(unittest.TestCase):
    def setUp(self):
        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass
            def reply(self, value, status=200):
                raw = json.dumps(value).encode()
                self.send_response(status)
                self.send_header('Content-Type', 'application/json')
                self.send_header('Content-Length', str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)
        class Base:
            def start_mock(self):
                self.server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
                self.server.daemon_threads = True
                self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
                self.thread.start()
        cache = SimpleNamespace(fixture_type=lambda _: Base)
        self.fixture = s.fixture_type(cache, None)()
        self.fixture.lock = threading.Lock()
        self.fixture.start_mock()
        self.port = self.fixture.server.server_address[1]

    def tearDown(self):
        self.fixture.server.shutdown()
        self.fixture.server.server_close()
        self.fixture.thread.join(timeout=2)

    def request(self, endpoint, mode='text', body=None):
        self.fixture.native_modes['offline'] = mode
        conn = http.client.HTTPConnection('127.0.0.1', self.port, timeout=3)
        self.addCleanup(conn.close)
        if body is None:
            body = {'contents': [{'role': 'user', 'parts': [{'text': 'synthetic'}]}]}
        conn.request('POST', '/gemini-supplier/offline/' + endpoint, json.dumps(body), {'Content-Type': 'application/json'})
        return conn.getresponse()

    def test_http_mock_rejects_model_tail_empty_turn_and_unpaired_tools(self):
        endpoint = 'v1beta/models/' + s.supplier.MODEL + ':generateContent'
        for contents in ([], [{'role': 'model', 'parts': [{'text': 'partial'}]}],
                         [{'role': 'user', 'parts': []}],
                         [{'role': 'user', 'parts': [{'text': '  '}]}],
                         [{'role': 'model', 'parts': [{'functionCall': {'id': 'a', 'name': 'read', 'args': {}}}]},
                          {'role': 'user', 'parts': [{'text': 'not a tool result'}]}],
                         [{'role': 'user', 'parts': [{'functionResponse': {'id': 'a', 'name': 'read', 'response': {}}}]}]):
            with self.subTest(contents=contents):
                response = self.request(endpoint, body={'contents': contents})
                self.assertEqual(response.status, 400)
                response.read()

    def test_http_mock_accepts_reverse_same_name_results_but_rejects_duplicate(self):
        contents = [
            {'role': 'model', 'parts': [{'functionCall': {'id': ident, 'name': 'read', 'args': {}}} for ident in ('a', 'b')]},
            {'role': 'user', 'parts': [{'functionResponse': {'id': ident, 'name': 'read', 'response': {'output': ''}}} for ident in ('b', 'a')]}]
        endpoint = 'v1beta/models/' + s.supplier.MODEL + ':generateContent'
        response = self.request(endpoint, body={'contents': contents})
        self.assertEqual(response.status, 200)
        response.read()
        contents[-1]['parts'].append(copy.deepcopy(contents[-1]['parts'][-1]))
        response = self.request(endpoint, body={'contents': contents})
        self.assertEqual(response.status, 400)
        response.read()

    def test_http_mock_rejects_anthropic_bridge_and_captures_actual_native_body(self):
        response = self.request('v1/messages')
        self.assertEqual(response.status, 400)
        response.read()
        expected = {'contents': [{'role': 'user', 'parts': [{'text': 'synthetic'}]}], 'generationConfig': {'maxOutputTokens': 1}}
        response = self.request('v1beta/models/' + s.supplier.MODEL + ':generateContent', body=expected)
        self.assertEqual(response.status, 200)
        self.assertEqual(json.loads(response.read())['candidates'][0]['content']['parts'][0]['text'], s.TEXT)
        self.assertEqual(self.fixture.native_calls[-1]['body'], expected)

    def test_http_stream_has_fragmented_calls_and_native_terminal(self):
        response = self.request('v1beta/models/' + s.supplier.MODEL + ':streamGenerateContent?alt=sse', 'tools')
        self.assertEqual(response.status, 200)
        rows = s.supplier.events(response.read().decode())
        self.assertEqual(len(rows), 3)
        self.assertEqual(rows[-1]['candidates'][0]['finishReason'], 'STOP')

    def test_thinking_mock_accepts_only_the_explicit_fixture_mapping(self):
        wrong = self.request('v1beta/models/' + s.supplier.MODEL + ':generateContent', 'thinking')
        self.assertEqual(wrong.status, 400)
        wrong.read()
        mapped = self.request('v1beta/models/' + s.THINKING_MODEL + ':generateContent', 'thinking')
        self.assertEqual(mapped.status, 200)
        self.assertTrue(json.loads(mapped.read())['candidates'][0]['content']['parts'][0]['thought'])

    def test_http_mock_physically_disconnects_before_declared_body_length(self):
        response = self.request('v1beta/models/' + s.supplier.MODEL + ':streamGenerateContent?alt=sse', 'disconnect')
        self.assertEqual(response.status, 200)
        with self.assertRaises(http.client.IncompleteRead):
            response.read()


if __name__ == '__main__':
    unittest.main()
