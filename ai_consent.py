"""Explicit, per-request consent for forwarding layout/context to an AI CLI."""
import sys


def require_consent(provider, model, state, *, granted=False, refused=False,
                    allow_prompt=None, read=input, write=print):
    destination = 'Claude' if provider == 'claude' else 'Codex'
    write(f'External AI request: {destination}, model {model}, through your installed signed-in CLI.')
    write(f"Data sent: {len(state['items'])} app/widget placements, app or shortcut identifiers and labels, "
          'folder/page/Dock structure, supplied widget/color metadata, and your free-text context/interview answers.')
    write('This data leaves your computer for the AI service configured by that CLI. '
          'This request only produces questions or a draft; it does not change the phone.')
    if refused:
        raise ValueError('AI sharing declined (--no-ai). Nothing sent; phone unchanged.')
    if granted:
        write('Sharing authorized for this request by --consent-ai.')
        return
    if allow_prompt is None:
        allow_prompt = sys.stdin.isatty()
    if not allow_prompt:
        raise ValueError('AI sharing needs explicit consent. Review the disclosure and use --consent-ai, '
                         'or use --no-ai to decline. No AI request was made.')
    try:
        answer = read('Send this information to the selected AI service? [y/N] ').strip().lower()
    except (EOFError, KeyboardInterrupt):
        answer = ''
    if answer not in ('y', 'yes'):
        raise ValueError('AI sharing declined. Nothing sent; phone unchanged.')
