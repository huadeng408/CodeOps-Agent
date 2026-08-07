# -*- coding: utf-8 -*-
import sqlite3, sys, datetime
sys.stdout.reconfigure(encoding='utf-8')
DB = r'C:\Users\ieeep\.codex\logs_2.sqlite'
db = sqlite3.connect('file:' + DB.replace('\\', '/') + '?mode=ro', uri=True)
cur = db.cursor()
def ts(sec):
    return datetime.datetime.fromtimestamp(sec).strftime('%m-%d %H:%M:%S') if sec else '-'
cur.execute("SELECT ts, level, feedback_log_body FROM logs WHERE feedback_log_body LIKE '%019fb125%' OR feedback_log_body LIKE '%thread/fork%' ORDER BY ts LIMIT 40")
for r in cur.fetchall():
    print(f'  {ts(r[0])} [{r[1]}]: {(r[2] or "")[:500]}')
print()
# check DB for 019fb125
db2 = sqlite3.connect('file:C:/Users/ieeep/.codex/state_5.sqlite?mode=ro', uri=True)
cur2 = db2.cursor()
cur2.execute("SELECT id, rollout_path, title, source, model_provider, created_at_ms FROM threads WHERE id LIKE '019fb125%' OR id LIKE '019fb126%' OR id LIKE '019fb127%'")
for r in cur2.fetchall():
    print('DB:', r[0], '|', (r[2] or '')[:40], '|', r[3], '|', r[4], '|', ts(r[5]/1000), '|', r[1].replace('\\','/')[-50:])
