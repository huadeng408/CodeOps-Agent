# -*- coding: utf-8 -*-
import sqlite3, os, sys, datetime, re
sys.stdout.reconfigure(encoding='utf-8')
DB = r'C:\Users\ieeep\.codex\logs_2.sqlite'
db = sqlite3.connect('file:' + DB.replace('\\', '/') + '?mode=ro', uri=True)
cur = db.cursor()
def ts(sec):
    return datetime.datetime.fromtimestamp(sec).strftime('%Y-%m-%d %H:%M:%S') if sec else '-'
# search for archive/delete/migrate events in log body
for kw in ('archiv', 'delet', 'migrat', 'provider', 'backfill'):
    cur.execute("SELECT ts, level, target, feedback_log_body FROM logs WHERE feedback_log_body LIKE ? ORDER BY ts LIMIT 12", ('%' + kw + '%',))
    rows = cur.fetchall()
    print(f'===== keyword: {kw} ({len(rows)} shown of matches) =====')
    for r in rows:
        body = (r[3] or '')[:220].replace('\n', ' ')
        print(f'  {ts(r[0])} [{r[1]}] {r[2][:50]}: {body}')
    print()
