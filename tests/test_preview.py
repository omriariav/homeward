import copy
import unittest

from planner import inventory
from preview import render


class PreviewTests(unittest.TestCase):
    def setUp(self):
        self.state = inventory([['phone'], [['Work', 'slack'], 'news', 'spare']],
                               {'apps': {'phone': {'label': 'Phone'}, 'slack': {'label': 'Slack'},
                                         'news': {'label': 'חדשות'}, 'spare': {'label': 'Spare'}}})
        self.artifact = {'status': 'draft', 'input': {'current_state': self.state,
                         'selection': 'curate', 'max_pages': 1},
                         'plan': {'summary': 'Keep essentials.', 'dock': ['item-0001'],
                                  'pages': [{'title': 'Essentials', 'reason': 'Work and news',
                                             'items': ['item-0002', 'item-0003']}],
                                  'hidden': [{'item': 'item-0004', 'reason': 'Lower relevance'}],
                                  'assumptions': ['Usage is unknown'], 'questions': ['Which news app?']}}

    def test_preview_exposes_selection_changes_and_uncertainty(self):
        output = render(self.artifact)
        for expected in ('NOT APPLIED', 'Current Dock: Phone', 'Proposed Dock',
                         'Page 1: Essentials', 'Page 1 / Work → Page 1, slot 1',
                         'Spare [item-0004] — Lower relevance', 'Usage is unknown',
                         'Which news app?', 'saved planning snapshot', 'No apps will be uninstalled'):
            self.assertIn(expected, output)

    def test_preview_revalidates_draft_before_display(self):
        broken = copy.deepcopy(self.artifact)
        broken['plan']['pages'][0]['items'].append('unknown')
        with self.assertRaises(ValueError):
            render(broken)
        broken = copy.deepcopy(self.artifact)
        broken['status'] = 'input-only'
        with self.assertRaisesRegex(ValueError, 'completed draft'):
            render(broken)

    def test_untrusted_labels_cannot_inject_terminal_controls(self):
        self.state['items'][1]['label'] = '\x1b[2JSlack\nFAKE\x07'
        output = render(self.artifact)
        self.assertNotIn('\x1b', output)
        self.assertNotIn('\x07', output)
        self.assertIn('Slack FAKE', output)

    def test_narrow_preview_preserves_full_unicode_names_in_legend(self):
        name = 'חדשות ארוכות מאוד מאוד מאוד'
        self.state['items'][2]['label'] = name
        output = render(self.artifact, columns=40)
        self.assertIn(name, output)
        self.assertIn('…', output)


if __name__ == '__main__':
    unittest.main()
