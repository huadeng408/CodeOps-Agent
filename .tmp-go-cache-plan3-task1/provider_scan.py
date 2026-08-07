# -*- coding: utf-8 -*-
import sqlite3, os, sys, json
sys.stdout.reconfigure(encoding='utf-8')
db = sqlite3.connect(r'file:C:/Users/ieeep/.codex/state_5.sqlite?mode=ro', uri=True)
cur = db.cursor()
cur.execute('SELECT id, rollout_path, model_provider, updated_at_ms FROM threads')
rows = cur.fetchall()
mismatch = []
nocheck = 0
for r in rows:
    p = r[1].replace('\\', '/')
    if not os.path.exists(p):
        nocheck += 1
        continue
    try:
        with open(p, 'r', encoding='utf-8', errors='replace') as f:
            first = f.readline()
        o = json.loads(first)
        meta = o.get('payload', {})
        rp = meta.get('model_provider')
    except Exception:
        continue
    if rp is not None and rp != r[2]:
        mismatch.append((r[0], r[2], rp, r[3]))
print('checked:', len(rows) - nocheck, '| no-file:', nocheck, '| mismatch DB-vs-rollout:', len(mismatch))
for m in mismatch:
    print(' ', m[0], '| DB:', m[1], '| rollout:', m[2])
