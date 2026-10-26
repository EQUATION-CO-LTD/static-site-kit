#!/usr/bin/env python3
"""pyfmt と sitegen のテストの期待値（py_golden.json）を、Python の標準ライブラリそのものから作る。

    python3 sitegen/pyfmt/testdata/gen_golden.py   # sitegen/pyfmt/testdata/py_golden.json を書く

Python の html・urllib.parse・json・re の振る舞いが「正解」で、Go の実装がそれと同じ出力になることを
テストで確かめる。標準ライブラリだけを使い、キットの外の物に依存しない。
"""
import html
import json
import os
import re
import sys
import urllib.parse
from xml.sax.saxutils import escape as xml_escape

HERE = os.path.dirname(os.path.abspath(__file__))

STRINGS = [
    '', 'plain', 'a & b', '<script>alert("x")</script>', "it's", 'a"b', '&amp; &lt; &#39;', '日本語のテキスト', 'サンプル株式会社',
    '絵文字😀と𠮷', 'tab\there', 'line1\nline2', 'crlf\r\nx', 'nbsp\xa0here', ' sep ', 'ctl\x01\x1f\x7f', 'back\\slash',
    '/path/to?x=1&y=2#h', '100%', ' lead and trail ', 'ｆｕｌｌ　ｗｉｄｔｈ', '</script></p>', 'ÀÉÎõü', '　全角空白　',
]

PLAIN_INPUTS = [
    '<p>こんにちは&nbsp;世界</p>', '<p>a</p>\n<p>b &amp; c</p>', 'x<br />y　z', '&#x27;quote&#39; &copy; &foo; &ampx', '', '   ',
    '<div>' + 'あ' * 200 + '</div>', '<a href="x">link</a>\xa0text end', 'emoji 😀 ' * 30,
]

URLS_QUOTE = ['images/a b.png', 'dir/日本語/ファイル名.jpg', 'a/b?c=d&e=f%20g', 'x~y_z-1.5', 'ç/é', 'plus+and:colon@at', '', '/', 'a/😀.png']
URLS_UNQUOTE = ['%E6%97%A5%E6%9C%AC', 'a%20b', '100%', '%zz', '%E3%81', '%E3%81x', '日本%E8%AA%9E', 'no escapes', '%F0%9F%98%80', '%c3%a9', '/a/%2e%2e/b', '%ff%fe', 'a%2', '%']
UNESCAPES = ['&amp;', '&lt;&gt;', '&quot;', '&#39;', '&#x27;', '&nbsp;', '&copy', '&copy;', '&ampx', '&notit;', '&foo;', '&#128;', '&#x80;', '&#0;', '&#1;',
             '&#xD800;', '&#1114112;', '&#65;', '&#x41;', '&#X41;', 'a &amp b', '&hellip;&mdash;', '&Aacute;', '&aacute', '&unknown', '&', '& ', '&#;', '&#x;', '&lt', '&LT;', '&AMP;',
             '&#8364;', '&euro;', '&yen;', '&#xA0;', '&ensp;&emsp;', '&apos;', '&amp;amp;', '&#38;#38;', '&#60;script&#62;']
FLOATS = ['0.1', '1.0', '1e-05', '1E+16', '1e16', '123456789.123', '1e22', '0.0001', '0.00001', '-0.0', '100.5e2', '1.7976931348623157e308', '5e-324', '12345678901234567890', '-7', '0', '-0', '3.14159', '1e5', '2.5E-3']

JSON_DOCS = [
    '{"b": 1, "a": [1, 2, {"z": null, "y": true, "x": false}], "c": {}, "d": [], "e": "日本語\\n\\t\\"q\\" \\u3042 \\ud83d\\ude00 </script> \\u007f \\u001f"}',
    '[]', '{}', '[[], {}, [[]], [{}]]', '"just a string"', '12', '{"k": 1.0, "f": 1e-05, "g": 1E+16, "big": 12345678901234567890, "neg": -0}',
    '{"dup": 1, "other": 2, "dup": 3}', '{"é": "ü", "😀": "😀", "<>&": "<>&\'"}',
]


def ranges(cps):
    out = []
    for c in cps:
        if out and out[-1][1] == c - 1:
            out[-1][1] = c
        else:
            out.append([c, c])
    return out


def plain(html_text, n=110):
    t = re.sub(r'<[^>]+>', ' ', html_text)
    return re.sub(r'\s+', ' ', html.unescape(t)).strip()[:n]


def main():
    g = {}
    g['esc'] = [{'in': s, 'out': html.escape(s, quote=True), 'br': html.escape(s, quote=True).replace('\r\n', '\n').replace('\n', '<br />\n')} for s in STRINGS]
    g['xml_escape'] = [{'in': s, 'out': xml_escape(s)} for s in STRINGS]
    g['plain'] = [{'in': s, 'out': plain(s)} for s in PLAIN_INPUTS]
    g['quote'] = [{'in': s, 'safe': safe, 'out': urllib.parse.quote(s, safe=safe)} for s in URLS_QUOTE for safe in ('/', '/?=&%')]
    g['unquote'] = [{'in': s, 'out': urllib.parse.unquote(s)} for s in URLS_UNQUOTE]
    g['unescape'] = [{'in': s, 'out': html.unescape(s)} for s in UNESCAPES]
    g['float_repr'] = [{'in': s, 'out': json.dumps(json.loads(s))} for s in FLOATS]
    g['json'] = []
    for doc in JSON_DOCS:
        v = json.loads(doc)
        g['json'].append({
            'in': doc,
            'pretty': json.dumps(v, ensure_ascii=False, indent=1),
            'pretty_ascii': json.dumps(v, ensure_ascii=True, indent=1),
            'compact': json.dumps(v, ensure_ascii=False, separators=(',', ':')).replace('</', '<\\/'),
            'compact_ascii': json.dumps(v, separators=(',', ':')),
            'default': json.dumps(v, ensure_ascii=False),
        })
    # 正規表現の \s と \w が、Go 側で定めた文字の集まりと一致するか（全コードポイントで比べるための期待値）
    g['space_ranges'] = ranges([c for c in range(0x110000) if re.match(r'\s', chr(c))])
    g['word_ranges'] = ranges([c for c in range(0x110000) if re.match(r'\w', chr(c))])
    with open(os.path.join(HERE, 'py_golden.json'), 'w', encoding='utf-8', newline='\n') as f:
        json.dump(g, f, ensure_ascii=False, indent=0)
        f.write('\n')
    print('python', sys.version.split()[0], 'unicode', __import__('unicodedata').unidata_version)


if __name__ == '__main__':
    main()
