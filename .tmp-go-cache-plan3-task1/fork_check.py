# -*- coding: utf-8 -*-
import sqlite3, os, sys, datetime, glob
sys.stdout.reconfigure(encoding='utf-8')
db = sqlite3.connect(r'file:C:/Users/ieeep/.codex/state_5.sqlite?mode=ro', uri=True)
cur = db.cursor()
def ts(ms):
    return datetime.datetime.fromtimestamp(ms/1000).strftime('%m-%d %H:%M:%S') if ms else '-'
# fork edges
cur.execute('SELECT * FROM thread_spawn_edges LIMIT 5')
cols = [d[0] for d in cur.description]
print('spawn_edges cols:', cols)
cur.execute("SELECT * FROM thread_spawn_edges WHERE parent_thread_id LIKE '019fab98%' OR child_thread_id LIKE '019fb125%' OR parent_thread_id LIKE '019fb125%'")
for r in cur.fetchall():
    print(' ', r)
# any thread with luna model or 019fb125
cur.execute("SELECT id, rollout_path, model, title, created_at_ms, source, model_provider FROM threads WHERE id LIKE '019fb12%' OR model LIKE '%luna%'")
rows = cur.fetchall()
print('threads 019fb12* or luna:', len(rows))
for r in rows:
    print(' ', r[0], '|', (r[2] or ''), '|', (r[3] or '')[:30], '|', ts(r[4]), '|', r[5][:15], '|', r[6])
# check rollout files for the fork
print()
for pat in (r'C:\Users\ieeep\.codex\sessions\2026\07\30\rollout-2026-07-30T11*',
            r'C:\Users\ieeep\.codex\archived_sessions\rollout-2026-07-30T11*'):
    print(pat, '->', glob.glob(pat))
