import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import interview
import planner
from homescreen import parser


class InterviewTests(unittest.TestCase):
    def setUp(self):
        self.questionnaire = {'summary': 'Keep work first and preserve personal interests.', 'questions': [
            {'id': 'page_priority', 'question': 'Which personal interest comes next?',
             'why': 'Sets the second page.', 'options': ['Family and spotting', 'Social and news']},
            {'id': 'sports', 'question': 'Which sports matter most?',
             'why': 'Prioritizes your existing sports apps.', 'options': []}]}
        self.session = {'version': 1, 'kind': 'homescreen-interview', 'status': 'pending',
            'request': 'Senior PM, father, sports and spotting with my son; no folders.',
            'current_state': planner.inventory([['slack'], [['Social', 'social'], 'sports']]),
            'provider': 'claude', 'model': 'claude-sonnet-5-5', 'effort': 'medium', 'style': 'purpose',
            'summary': self.questionnaire['summary'],
            'questions': [{**q, 'status': 'pending', 'answer': None} for q in self.questionnaire['questions']]}

    def test_rejects_duplicate_ids_and_excess_questions(self):
        bad = copy.deepcopy(self.questionnaire)
        bad['questions'][1]['id'] = bad['questions'][0]['id']
        with self.assertRaises(ValueError): interview.validate_questionnaire(bad, 4)
        with self.assertRaises(ValueError): interview.validate_questionnaire(self.questionnaire, 1)

    def test_choices_free_text_and_skips_save_without_defaults(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'interview.json'
            interview.save_session(path, self.session, new=True)
            answers = iter(['9', '1', ''])
            interview.collect_answers(self.session, path, lambda _: next(answers), lambda _: None)
            saved = interview.load_session(path)
            self.assertEqual(saved['status'], 'complete')
            self.assertEqual(saved['questions'][0]['answer'], 'Family and spotting')
            self.assertEqual(saved['questions'][1]['status'], 'skipped')
            self.assertIsNone(saved['questions'][1]['answer'])

    def test_interrupt_preserves_answer_and_resume_does_not_reask_it(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'interview.json'
            interview.save_session(path, self.session, new=True)
            with patch('builtins.input', side_effect=['My custom preference', EOFError]):
                # Pass the patched reader explicitly; no real terminal required.
                interview.collect_answers(self.session, path, input, lambda _: None)
            saved = interview.load_session(path)
            self.assertEqual(saved['status'], 'pending')
            self.assertEqual(saved['questions'][0]['answer'], 'My custom preference')
            reads = []
            interview.collect_answers(saved, path, lambda prompt: reads.append(prompt) or 'F1', lambda _: None)
            self.assertEqual(len(reads), 1)
            self.assertEqual(interview.load_session(path)['status'], 'complete')

    def test_answers_validation_is_transactional_and_cannot_assume_blank(self):
        before = copy.deepcopy(self.session)
        for answers in ({'unknown': 'x'}, {'page_priority': 'ok', 'sports': ''}, {'sports': 1}):
            with self.assertRaises(ValueError): interview.set_answers(self.session, answers)
            self.assertEqual(self.session, before)

    def test_pending_interview_cannot_be_planned_even_if_status_is_forged(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'interview.json'
            self.session['status'] = 'complete'
            path.write_text(json.dumps(self.session))
            args = parser().parse_args(['plan', '--interview', str(path), '--output', str(Path(tmp)/'plan.json')])
            with self.assertRaisesRegex(ValueError, 'incomplete'): planner.run(args)

    def test_interview_context_and_answers_reach_planner_unchanged(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'interview.json'
            interview.set_answers(self.session, {'page_priority': 'Family and spotting', 'sports': 'F1'})
            interview.save_session(path, self.session, new=True)
            output = Path(tmp) / 'plan.json'
            args = parser().parse_args(['plan', '--interview', str(path), '--request', 'Calendar in Dock.',
                                       '--dry-run', '--output', str(output)])
            planner.run(args)
            data = json.loads(output.read_text())
            self.assertEqual(data['input']['request'], self.session['request'])
            self.assertEqual(data['input']['interview_answers'], self.session['questions'])
            self.assertEqual(data['input']['additional_request'], 'Calendar in Dock.')
            self.assertEqual(data['input']['current_state'], self.session['current_state'])

    def test_questions_only_and_resume_answers_do_not_invoke_device_or_regenerate(self):
        with tempfile.TemporaryDirectory() as tmp:
            layout = Path(tmp)/'layout.json'; layout.write_text('[["slack"],["social"]]')
            path = Path(tmp)/'interview.json'
            args = parser().parse_args(['interview', '--consent-ai', '--layout', str(layout), '--request', 'Work first',
                                        '--questions-only', '--output', str(path)])
            with patch('interview.ask_json', return_value=self.questionnaire) as model:
                interview.run(args, read=lambda _: self.fail('Must not read stdin'), write=lambda _: None)
                self.assertIn('Work first', model.call_args.args[3])
                self.assertIn('social', model.call_args.args[3])
            answers = Path(tmp)/'answers.json'; answers.write_text('{"page_priority":"Social and news","sports":null}')
            args = parser().parse_args(['interview', '--resume', str(path), '--answers', str(answers)])
            with patch('interview.ask_json', side_effect=AssertionError('Must not regenerate')):
                interview.run(args, read=lambda _: self.fail('Must not read stdin'), write=lambda _: None)
            self.assertEqual(interview.load_session(path)['status'], 'complete')

    def test_resume_cannot_replace_saved_request_or_inventory(self):
        args = parser().parse_args(['interview', '--resume', '/unused', '--request', 'replace context'])
        with self.assertRaisesRegex(ValueError, 'saved context'): interview.run(args)


if __name__ == '__main__':
    unittest.main()
