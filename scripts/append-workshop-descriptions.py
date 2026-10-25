"""Append user-workspace Workshop description snapshots without rewriting prose.

Only Steam BBCode headings/lists/links/bold/code are converted to Markdown.
Run with --check to verify the complete appendix and its raw snapshot hashes.
"""
import argparse
import hashlib
import json
import pathlib
import re
import shutil

ROOT = pathlib.Path(__file__).resolve().parents[1]
DOCUMENT = ROOT / 'docs/使用教程.md'
SOURCES = ROOT / 'docs/sources/workshop'
MARKER = '<!-- workshop-descriptions:begin -->'
END = '<!-- workshop-descriptions:end -->'
PLATFORMS = [('3787239937', 'Skeeto'), ('3785109042', 'L4D2Evolved（Evo）')]


def sha256(data):
    return hashlib.sha256(data).hexdigest()


def visible_source(text):
    text = re.sub(r'\[(?:/?h[12]|/?b|/?list|/?olist|/?code|\*|url=[^\]]+|/url)\]', '', text)
    return ' '.join(text.split())


def visible_markdown(text):
    text = re.sub(r'\[(.*?)\]\((https?://[^)]+)\)', r'\1', text)
    text = re.sub(r'^#{1,6}\s+|^\d+\.\s+|^-\s+|^```(?:text)?\s*$', '', text, flags=re.M)
    return ' '.join(text.replace('**', '').split())


def markdown(text):
    if not re.search(r'\[(?:h[12]|list|olist|b|code|url=)', text):
        return text.strip()
    original = text
    text = re.sub(r'\[url=([^\]]+)\](.*?)\[/url\]', r'[\2](\1)', text, flags=re.S)
    text = re.sub(r'\[h1\](.*?)\[/h1\]', r'#### \1', text, flags=re.S)
    text = re.sub(r'\[h2\](.*?)\[/h2\]', r'##### \1', text, flags=re.S)
    text = text.replace('[b]', '**').replace('[/b]', '**')
    text = re.sub(r'\[code\](.*?)\[/code\]', lambda m: '\n```text\n' + m[1] + '\n```\n', text, flags=re.S)
    lines, stack = [], []
    for line in text.splitlines():
        tag = line.strip()
        if tag in ('[list]', '[olist]'):
            stack.append([tag == '[olist]', 0])
            lines.append('')
        elif tag in ('[/list]', '[/olist]'):
            assert stack, 'Unbalanced list in snapshot'
            stack.pop()
            lines.append('')
        elif line.startswith('[*]'):
            assert stack, 'List item outside a list'
            stack[-1][1] += 1
            prefix = str(stack[-1][1]) + '. ' if stack[-1][0] else '- '
            lines.append(prefix + line[3:])
        else:
            lines.append(line)
    assert not stack, 'Unclosed list in snapshot'
    result = '\n'.join(lines).strip()
    assert visible_source(original) == visible_markdown(result), 'Description wording changed'
    for link in re.findall(r'\[url=([^\]]+)\]', original):
        assert '](' + link + ')' in result, 'Original link missing'
    assert not re.search(r'\[/?(?:h[12]|b|list|olist|code|url)(?:=|\])', result), 'Unconverted BBCode'
    return result


def appendix(records):
    parts = [MARKER, '## 附录：Skeeto 与 Evo 工坊页面说明原文',
             '> 以下原文复制自用户本地已保存的 Steam 官方详情 API `description` 快照（2026-10-25），不是本次成功读取的网页 HTML，也不声称是实时页面。仅调整标题、列表、链接与代码块排版，不改写说明正文。',
             '> 原文中的兼容性、反作弊及安全性说法属于作者陈述，并非本工具的验证结论；原文的一般安装方式与本工具的独立部署路线有区别，使用本工具时以教程前文为准。']
    for ident, name in PLATFORMS:
        entry = next(row for row in records['descriptions'] if row['id'] == ident)
        data = (SOURCES / entry['file']).read_bytes()
        assert sha256(data) == entry['sha256'], f'Snapshot changed: {ident}'
        source = data.decode('utf-8-sig').replace('\r\n', '\n')
        parts.extend(['---', '### ' + name, '**Mod 页面地址：** ' + entry['page'],
                      '<!-- source-description-sha256: ' + entry['sha256'] + ' -->', markdown(source)])
    parts.append(END)
    return '\n\n'.join(parts) + '\n'


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--import-from', type=pathlib.Path, help='Existing local inspection snapshots directory')
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    assert not (args.check and args.import_from), '--check does not import or write files'
    if args.import_from:
        SOURCES.mkdir(parents=True, exist_ok=True)
        rows = []
        for ident, name in PLATFORMS:
            src = args.import_from / ident / 'workshop_description_from_api.txt'
            dest = SOURCES / (ident + '-description.txt')
            shutil.copyfile(src, dest)
            rows.append(dict(id=ident, name=name, page='https://steamcommunity.com/sharedfiles/filedetails/?id=' + ident,
                             file=dest.name, sha256=sha256(dest.read_bytes()), bytes=dest.stat().st_size))
        (SOURCES / 'sources.json').write_text(json.dumps(dict(snapshot_utc='2026-10-25T15:30:22.662818Z',
                source='Existing user-workspace Steam official API description snapshots; HTML fetch unsuccessful',
                descriptions=rows), ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    records = json.loads((SOURCES / 'sources.json').read_text(encoding='utf-8'))
    current = DOCUMENT.read_text(encoding='utf-8')
    assert current.splitlines()[0] == '如果检查更新功能未正常工作，则请自备魔法使用。'
    assert current.count(MARKER) <= 1 and current.count(END) <= 1, 'Duplicate appendix'
    expected = appendix(records)
    if args.check:
        assert MARKER in current and current[current.index(MARKER):] == expected, 'Appendix does not match source snapshots'
        print('PASS: 2 complete descriptions; source hashes, wording, original links, formatting and first line verified.')
    else:
        prefix = current.split(MARKER, 1)[0].rstrip()
        DOCUMENT.write_text(prefix + '\n\n' + expected, encoding='utf-8', newline='\n')
        print('Appended two complete local description snapshots; prose preserved, BBCode formatted.')


if __name__ == '__main__':
    main()
