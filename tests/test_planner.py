import argparse
import copy
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from planner import inventory, validate_plan, command_for, decode_claude, run, verify_layout
from homescreen import parser


class PlannerTests(unittest.TestCase):
    def setUp(self):
        self.layout = [['slack'], [['Social', 'instagram', 'messages'], ['News', ['news1'], ['news2']], 'tasks']]
        self.state = inventory(self.layout)
        ids = [i['id'] for i in self.state['items']]
        self.plan = {'summary': 'Work first, personal later.', 'dock': ids[:1],
                     'pages': [{'title': 'Work', 'reason': 'Focus', 'items': ids[-1:]},
                               {'title': 'Personal', 'reason': 'Preserve social and news', 'items': ids[1:-1]}],
                     'hidden': [], 'assumptions': [], 'questions': []}

    def test_folder_context_and_multipage_folders_are_retained(self):
        self.assertEqual(self.state['items'][1]['source_folders'], ['Social'])
        self.assertEqual(self.state['items'][4]['source_folders'], ['News'])
        self.assertEqual(len(self.state['items']), 6)
        validate_plan(self.plan, self.state)

    def test_work_focus_cannot_silently_drop_social_apps(self):
        broken = copy.deepcopy(self.plan)
        broken['pages'][1]['items'].pop()
        with self.assertRaisesRegex(ValueError, 'every input item'):
            validate_plan(broken, self.state)

    def test_hiding_requires_explicit_id_allowance(self):
        plan = copy.deepcopy(self.plan)
        item = plan['pages'][1]['items'].pop()
        plan['hidden'] = [{'item': item, 'reason': 'Requested'}]
        with self.assertRaisesRegex(ValueError, 'without explicit'):
            validate_plan(plan, self.state)
        validate_plan(plan, self.state, [item])

    def test_curate_accounts_for_exclusions_and_protects_required_apps(self):
        plan = copy.deepcopy(self.plan)
        removed = plan['pages'].pop()['items']
        plan['hidden'] = [{'item': item, 'reason': 'Lower relevance to stated work focus'} for item in removed]
        validate_plan(plan, self.state, selection='curate', max_pages=1,
                      required_visible=self.plan['dock'])
        with self.assertRaisesRegex(ValueError, 'required-visible'):
            validate_plan(plan, self.state, selection='curate', required_visible=[removed[0]])
        plan['hidden'][0]['reason'] = ' '
        with self.assertRaisesRegex(ValueError, 'reason'):
            validate_plan(plan, self.state, selection='curate')

    def test_page_budget_is_enforced(self):
        with self.assertRaisesRegex(ValueError, 'page budget'):
            validate_plan(self.plan, self.state, selection='curate', max_pages=1)

    def test_device_verification_rejects_repopulated_apps_and_reordering(self):
        expected = [['phone'], ['slack', 'news']]
        verify_layout(expected, copy.deepcopy(expected))
        for actual in ([['phone'], ['slack', 'news', 'extra']],
                       [['phone'], ['slack', 'news'], ['extra']],
                       [['phone'], ['news', 'slack']],
                       [['phone'], ['slack']]):
            with self.assertRaisesRegex(ValueError, 'differs'):
                verify_layout(expected, actual)

    def test_unknown_and_duplicate_output_ids_rejected(self):
        for bad in ('invented', self.plan['dock'][0]):
            plan = copy.deepcopy(self.plan)
            plan['pages'][1]['items'][0] = bad
            with self.assertRaises(ValueError):
                validate_plan(plan, self.state)

    def test_duplicate_input_occurrences_keep_distinct_ids(self):
        state = inventory([['same'], ['same']])
        self.assertNotEqual(state['items'][0]['id'], state['items'][1]['id'])

    def test_malformed_response_rejected(self):
        for bad in ({}, {'unexpected': True}, []):
            with self.assertRaises(ValueError):
                validate_plan(bad, self.state)
        with self.assertRaises(ValueError):
            decode_claude('{"subtype":"error","is_error":true}')

    def test_headless_provider_flags_and_safe_argument_transport(self):
        claude = command_for('claude', 'claude-sonnet-5-5', 'medium', '/tmp/test')
        self.assertIn('-p', claude)
        self.assertIn('--safe-mode', claude)
        self.assertEqual(claude[claude.index('--tools') + 1], '')
        codex = command_for('codex', 'gpt-6.1-sol', 'medium', '/tmp/test')
        self.assertEqual(codex[:2], ['codex', 'exec'])
        self.assertIn('read-only', codex)
        self.assertIn('model_reasoning_effort="medium"', codex)
        self.assertEqual(codex[-1], '-')

    def test_widget_and_color_metadata_survive_normalization(self):
        state = inventory([[], ['app']], {'apps': {'app': {'label': 'App', 'color': 'blue'}},
                                          'widgets': [{'label': 'Clock', 'size': 'small', 'page': 2}]})
        self.assertEqual(state['items'][0]['color'], 'blue')
        self.assertEqual(state['items'][1]['kind'], 'widget')
        plan = {'summary': '', 'dock': [state['items'][1]['id']],
                'pages': [{'title': '', 'reason': '', 'items': [state['items'][0]['id']]}],
                'hidden': [], 'assumptions': [], 'questions': []}
        with self.assertRaisesRegex(ValueError, 'Dock'):
            validate_plan(plan, state)

    def test_mocked_live_run_sends_context_over_stdin_and_validates(self):
        with tempfile.TemporaryDirectory() as temp:
            layout = Path(temp) / 'layout.json'; layout.write_text(json.dumps(self.layout))
            output = Path(temp) / 'draft.json'
            args = parser().parse_args(['plan', '--consent-ai', '--layout', str(layout), '--output', str(output),
                                       '--request', 'Work focus; keep social. $(do not execute)'])
            envelope = {'subtype': 'success', 'structured_output': self.plan}
            with patch('planner.subprocess.run', return_value=subprocess.CompletedProcess([], 0, json.dumps(envelope), '')) as runner:
                run(args)
                call = runner.call_args
                self.assertIn('Social', call.kwargs['input'])
                self.assertIn('$(do not execute)', call.kwargs['input'])
                self.assertNotIn('$(do not execute)', ' '.join(call.args[0]))
                self.assertNotIn('shell', call.kwargs)
            saved = json.loads(output.read_text())
            self.assertEqual(saved['status'], 'draft')
            self.assertEqual(saved['plan'], self.plan)

    def test_failed_provider_never_saves_a_plan(self):
        with tempfile.TemporaryDirectory() as temp:
            layout = Path(temp) / 'layout.json'; layout.write_text(json.dumps(self.layout))
            output = Path(temp) / 'draft.json'
            args = parser().parse_args(['plan', '--consent-ai', '--layout', str(layout), '--output', str(output), '--request', 'Work'])
            with patch('planner.subprocess.run', return_value=subprocess.CompletedProcess([], 1, '', 'failure')):
                with self.assertRaises(ValueError): run(args)
            self.assertFalse(output.exists())


if __name__ == '__main__':
    unittest.main()
