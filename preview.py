"""Read-only terminal review of a validated layout draft. No device or AI calls."""
import json
import shutil
import textwrap
import unicodedata
from pathlib import Path

from planner import validate_plan


def safe(value):
    # App names and model text must not emit terminal escape/control sequences.
    return ''.join(c if not unicodedata.category(c).startswith('C') else ' ' for c in str(value))


def width(value):
    return sum(0 if unicodedata.combining(c) else 2 if unicodedata.east_asian_width(c) in 'WF' else 1
               for c in value)


def cell(value, size):
    value = safe(value)
    if width(value) > size:
        shortened = ''
        for char in value:
            if width(shortened + char) > size - 1:
                break
            shortened += char
        value = shortened + '…'
    return value + ' ' * max(0, size - width(value))


def source(item):
    page = item.get('source_page')
    location = 'Dock' if page == 0 else f'Page {page}' if page is not None else 'Widget location unknown'
    if item.get('source_folders'):
        location += ' / ' + ' / '.join(item['source_folders'])
    path = item.get('source_path', [])
    if len(path) == 2:
        location += f', slot {path[-1] + 1}'
    return safe(location)


def render(artifact, columns=100):
    if artifact.get('status') != 'draft' or not artifact.get('plan'):
        raise ValueError('Preview needs a completed draft, not input-only data.')
    payload = artifact['input']
    state = payload['current_state']
    plan = validate_plan(artifact['plan'], state, payload.get('allowed_hidden', []),
                         selection=payload.get('selection', 'preserve'),
                         max_pages=payload.get('max_pages'),
                         required_visible=payload.get('required_visible', []))
    items = {item['id']: item for item in state['items']}
    columns = max(40, columns)
    lines = ['PROPOSED HOME SCREEN — NOT APPLIED',
             'Compared with the saved planning snapshot; this is not a live device read.', '']

    def paragraph(text):
        lines.extend(textwrap.wrap(safe(text), width=columns) or [''])

    def label(key):
        item = items[key]
        return ('[widget] ' if item['kind'] == 'widget' else '') + safe(item.get('label', key))

    old_pages = {i.get('source_page') for i in items.values() if i.get('source_page', 0) > 0}
    selected = len(plan['dock']) + sum(len(p['items']) for p in plan['pages'])
    paragraph(plan['summary'])
    paragraph(f"Pages: {len(old_pages)} → {len(plan['pages'])} | Selected placements: {selected} | "
              f"Off Home Screen: {len(plan['hidden'])} | Proposed folders: 0")
    paragraph('No apps will be uninstalled. Shortcuts and widgets need separate review; they cannot simply be sent to App Library.')
    lines.append('')

    def grid(title, keys, reason=''):
        lines.append(safe(title))
        if reason:
            paragraph(reason)
        if not keys:
            lines.append('  (empty)')
            return
        # Preserve the four-column iPhone order, even on a narrow terminal.
        size = max(6, (columns - 5) // 4)
        for start in range(0, len(keys), 4):
            row = keys[start:start + 4]
            lines.append('|' + '|'.join(cell(f'{start + n + 1}. {label(key)}', size)
                                        for n, key in enumerate(row)) + '|')
        # Untruncated names remain available regardless of terminal width.
        for n, key in enumerate(keys, 1):
            paragraph(f'  {n}. {label(key)} [{key}]')
        lines.append('')

    old_dock = [i['id'] for i in state['items'] if i.get('source_page') == 0]
    paragraph('Current Dock: ' + (' | '.join(label(k) for k in old_dock) or '(empty)'))
    grid('Proposed Dock', plan['dock'])
    destinations = {}
    for n, key in enumerate(plan['dock']):
        destinations[key] = (0, n, f'Dock, slot {n + 1}')
    for page_number, page in enumerate(plan['pages'], 1):
        grid(f"Page {page_number}: {page['title']}", page['items'], page['reason'])
        for slot, key in enumerate(page['items']):
            destinations[key] = (page_number, slot, f'Page {page_number}, slot {slot + 1}')

    lines.append('MOVES / FOLDER CHANGES')
    moves = 0
    for key, (page, slot, destination) in destinations.items():
        item = items[key]
        if item.get('source_path') != [page, slot] or item.get('source_folders'):
            paragraph(f'{label(key)} [{key}]: {source(item)} → {destination}')
            moves += 1
    if not moves:
        lines.append('  None.')
    lines.extend(['', 'OFF HOME SCREEN — REVIEW EXCLUSIONS'])
    for entry in plan['hidden']:
        key = entry['item']
        paragraph(f"{label(key)} [{key}] — {entry['reason']} (from {source(items[key])})")
    if not plan['hidden']:
        lines.append('  None. Every supplied item remains on the Home Screen.')
    for title, entries in [('ASSUMPTIONS', plan['assumptions']),
                           ('OPEN QUESTIONS', plan['questions']),
                           ('LIMITATIONS', list(dict.fromkeys(state.get('warnings', []) + artifact.get('warnings', []))))]:
        if entries:
            lines.extend(['', title])
            for entry in entries:
                paragraph('- ' + entry)
    lines.extend(['', 'Icon order preview only; widget dimensions and free-position geometry are not simulated.',
                  'Read-only review. Nothing applied. Revise the draft if needed.',
                  'The integrated apply flow is not implemented; this preview does not authorize device changes.'])
    return '\n'.join(lines) + '\n'


def run(path):
    artifact = json.loads(Path(path).read_text())
    print(render(artifact, shutil.get_terminal_size((100, 24)).columns), end='')
