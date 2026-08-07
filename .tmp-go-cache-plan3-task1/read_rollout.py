# -*- coding: utf-8 -*-
import json, sys, datetime
sys.stdout.reconfigure(encoding='utf-8')
P = r'C:\Users\ieeep\.codex\sessions\2026\07\29\rollout-2026-07-29T16-17-53-019facf3-998b-79f2-a253-1b6596fa6952.jsonl'
lines = open(P, encoding='utf-8', errors='replace').readlines()
print('total lines:', len(lines))
def ts(t):
    if not t: return ''
    try:
        return datetime.datetime.fromtimestamp(float(t)).strftime('%m-%d %H:%M:%S')
    except Exception:
        return str(t)[:19]
# summarize: role + timestamp + first 120 chars
for i, ln in enumerate(lines):
    try:
        o = json.loads(ln)
    except Exception:
        continue
    role = o.get('type') or o.get('role') or '?'
    t = ts(o.get('timestamp'))
    if role in ('response_item',):
        p = o.get('payload', {})
        role = p.get('type', '?')
        if role == 'message':
            msg = p.get('role', '?')
            content = p.get('content', [])
            txt = ''
            for c in content:
                if isinstance(c, dict):
                    txt += (c.get('text') or c.get('input_text') or '')
            if not txt:
                continue
            print(f'[{i}] {t} {msg}: {txt[:150]}')
        elif role == 'function_call':
            print(f'[{i}] {t} FUNCTION_CALL: {p.get("name", "")}({str(p.get("arguments", ""))[:200]})')
        elif role == 'function_call_output':
            out = str(p.get('output', ''))[:150]
            print(f'[{i}] {t} FUNCTION_OUT: {out}')
    elif role in ('user', 'assistant'):
        # old format
        txt = ''
        c = o.get('content', [])
        if isinstance(c, str):
            txt = c
        elif isinstance(c, list):
            for cc in c:
                if isinstance(cc, dict):
                    txt += cc.get('text') or ''
        print(f'[{i}] {t} {role}: {txt[:150]}')
