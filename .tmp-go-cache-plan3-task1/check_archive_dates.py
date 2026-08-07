# -*- coding: utf-8 -*-
import sqlite3, os, datetime, sys, collections
sys.stdout.reconfigure(encoding='utf-8')
DB = r'C:\Users\ieeep\.codex\state_5.sqlite'
db = sqlite3.connect('file:' + DB.replace('\\', '/') + '?mode=ro', uri=True)
cur = db.cursor()

cur.execute('SELECT archived_at, COUNT(*) FROM threads WHERE archived=1 GROUP BY archived_at ORDER BY COUNT(*) DESC')
print('archived_at value distribution (archived=1):')
for r in cur.fetchall():
    print('  raw =', r[0], '| count =', r[1])

cur.execute('SELECT COUNT(*) FROM threads WHERE archived=1 AND archived_at IS NULL')
print('archived with NULL archived_at:', cur.fetchone()[0])

print()
cur.execute('SELECT * FROM backfill_state')
print('backfill_state rows:')
for r in cur.fetchall():
    print(' ', r)

print()
cur.execute("SELECT name FROM _sqlx_migrations ORDER BY rowid DESC LIMIT 10")
print('last 10 migrations:')
for r in cur.fetchall():
    print(' ', r[0])
