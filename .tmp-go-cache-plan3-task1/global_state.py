# -*- coding: utf-8 -*-
import sqlite3, os, sys, datetime
sys.stdout.reconfigure(encoding='utf-8')
DB = r'C:\Users\ieeep\.codex\state_5.sqlite'
db = sqlite3.connect('file:' + DB.replace('\\', '/') + '?mode=ro', uri=True)
cur = db.cursor()
cur.execute("SELECT id, rollout_path, archived FROM threads WHERE rollout_path LIKE '%archived_sessions%'")
rows = cur.fetchall()
print('threads pointing into archived_sessions:', len(rows))
for r in rows[:10]:
    print(' ', r[0][:24], '| arch:', r[2], '|', r[1].replace('\\','/')[-70:])

# check session_index.jsonl
print()
print('--- session_index.jsonl (last 5 lines) ---')
idx = r'C:\Users\ieeep\.codex\session_index.jsonl'
lines = open(idx, encoding='utf-8', errors='replace').readlines()
for ln in lines[-5:]:
    print(ln[:200])
print('total lines:', len(lines))

# global state file
print()
print('--- .codex-global-state.json top-level keys ---')
gs = r'C:\Users\ieeep\.codex\.codex-global-state.json'
import json
data = json.load(open(gs, encoding='utf-8'))
print('keys:', list(data.keys())[:20])
for k in list(data.keys())[:20]:
    v = data[k]
    if isinstance(v, list):
        print(' ', k, '= list len', len(v))
    elif isinstance(v, dict):
        print(' ', k, '= dict keys', list(v.keys())[:10])
    else:
        print(' ', k, '=', str(v)[:80])
