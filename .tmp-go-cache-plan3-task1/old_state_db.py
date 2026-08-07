# -*- coding: utf-8 -*-
import sqlite3, sys, datetime, os
sys.stdout.reconfigure(encoding='utf-8')
DB = r'C:\Users\ieeep\.codex\sqlite\state_5.sqlite'
print('exists:', os.path.exists(DB), 'size:', os.path.getsize(DB) if os.path.exists(DB) else '-')
db = sqlite3.connect('file:' + DB.replace('\\', '/') + '?mode=ro', uri=True)
cur = db.cursor()
cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
print('tables:', [r[0] for r in cur.fetchall()])
def ts(sec):
    return datetime.datetime.fromtimestamp(sec).strftime('%Y-%m-%d %H:%M:%S') if sec else '-'
try:
    cur.execute("SELECT COUNT(*) FROM threads")
    print('threads count:', cur.fetchone()[0])
    cur.execute("SELECT id, rollout_path, created_at_ms, updated_at_ms, archived, archived_at, title, model_provider FROM threads WHERE id LIKE '019fab98%' OR id LIKE '019facf3%' OR id LIKE '019e4080%' OR id LIKE '019fab95%' ORDER BY created_at_ms")
    rows = cur.fetchall()
    for r in rows:
        print(' ', r[0], '|', ts(r[3]/1000) if r[3] else '-', '| arch:', r[4], ts(r[5]) if r[5] else '-', '|', r[7], '|', (r[6] or '')[:30])
except Exception as e:
    print('ERR:', e)
