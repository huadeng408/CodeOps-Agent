# -*- coding: utf-8 -*-
import sqlite3, os, sys
sys.stdout.reconfigure(encoding='utf-8')
db = sqlite3.connect(r'file:C:/Users/ieeep/.codex/state_5.sqlite?mode=ro', uri=True)
cur = db.cursor()
for tid in ('019fab95-5593-7370-b218-808e12500831','019e4080-0c58-7f30-bc74-9d70a3b32589',
            '019e451b-36de-7803-8172-aa29d2e438b3','019e886f-f8b3-7863-9a62-5e7b7e3fb091',
            '019e8871-1e27-7a73-8ce0-c74b219a4fab','019eab14-5594-7171-a399-0dd74525ab8c'):
    cur.execute('SELECT rollout_path, archived FROM threads WHERE id=?', (tid,))
    r = cur.fetchone()
    p = r[0].replace('\\', '/')
    print(tid[:20], '| archived:', r[1])
    print('   ', p)
    print('    exists:', os.path.exists(p))
