import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from ai_consent import require_consent
from homescreen import parser
import planner
import interview


class ConsentTests(unittest.TestCase):
    def test_blank_no_and_interrupt_decline(self):
        for answer in ('', 'n', 'no', 'perhaps'):
            with self.assertRaisesRegex(ValueError, 'declined'):
                require_consent('claude', 'model', {'items': []}, allow_prompt=True,
                                read=lambda _: answer, write=lambda _: None)
        with self.assertRaises(ValueError):
            require_consent('claude', 'model', {'items': []}, allow_prompt=True,
                            read=lambda _: (_ for _ in ()).throw(KeyboardInterrupt()), write=lambda _: None)

    def test_noninteractive_requires_flag_and_discloses_destination(self):
        notice = []
        with self.assertRaisesRegex(ValueError, 'explicit consent'):
            require_consent('codex', 'gpt-6.1-sol', {'items': []}, allow_prompt=False, write=notice.append)
        self.assertIn('Codex', notice[0])
        self.assertTrue(any('leaves your computer' in x for x in notice))
        require_consent('codex', 'model', {'items': []}, granted=True, allow_prompt=False, write=lambda _: None)

    def test_refusal_blocks_both_model_paths_before_call(self):
        with tempfile.TemporaryDirectory() as tmp:
            layout = Path(tmp)/'layout.json'; layout.write_text('[["phone"],["work"]]')
            for command in ('plan', 'interview'):
                output = Path(tmp)/f'{command}.json'
                args = parser().parse_args([command, '--layout', str(layout), '--request', 'Work first',
                                            '--output', str(output), '--no-ai'])
                with patch(f'{command if command == "interview" else "planner"}.ask_json', side_effect=AssertionError('Must not send')):
                    with self.assertRaisesRegex(ValueError, 'declined'):
                        (interview if command == 'interview' else planner).run(args)
                self.assertFalse(output.exists())

    def test_no_ai_still_allows_local_dry_run(self):
        with tempfile.TemporaryDirectory() as tmp:
            layout = Path(tmp)/'layout.json'; layout.write_text('[["phone"],["work"]]')
            output = Path(tmp)/'draft.json'
            args = parser().parse_args(['plan', '--layout', str(layout), '--request', 'Work',
                                        '--output', str(output), '--no-ai', '--dry-run'])
            with patch('planner.ask_json', side_effect=AssertionError('Must not send')):
                planner.run(args)
            self.assertEqual(json.loads(output.read_text())['status'], 'input-only')


if __name__ == '__main__':
    unittest.main()
