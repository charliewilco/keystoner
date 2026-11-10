#!/usr/bin/env python3
"""Build a standalone, validated release from the checked-in SQL seed."""
import argparse
import hashlib
import json
import pathlib
import re
import sqlite3
import tempfile

SCHEMA_VERSION = 1

def validate(db):
    if db.execute('PRAGMA integrity_check').fetchall() != [('ok',)]:
        raise ValueError('integrity check failed')
    if db.execute('PRAGMA foreign_key_check').fetchall():
        raise ValueError('foreign key check failed')
    if not db.execute('SELECT count(*) FROM benefits').fetchone()[0]:
        raise ValueError('empty catalog')
    if db.execute('SELECT benefit_id FROM benefits EXCEPT SELECT benefit_id FROM benefit_details').fetchall():
        raise ValueError('missing cost/funding details')
    if db.execute('SELECT benefit_id FROM benefits EXCEPT SELECT benefit_id FROM benefit_sources').fetchall():
        raise ValueError('unsourced benefit')
    for url, date in db.execute('SELECT url,checked_on FROM sources'):
        if not url.startswith('https://') or not re.fullmatch(r'\d{4}-\d{2}-\d{2}', date):
            raise ValueError('invalid source URL/review date')
    for table, fields in [('benefits', ['eligibility','caveats','evidence_status','checked_on']), ('benefit_details', ['price_notes','funding_basis','availability'])]:
        for field in fields:
            if db.execute(f'SELECT 1 FROM {table} WHERE trim({field}) = ? LIMIT 1', ('',)).fetchone():
                raise ValueError('missing '+field)

def publish(seed, output, release, published):
    if not re.fullmatch(r'[a-zA-Z0-9][a-zA-Z0-9._-]{0,79}', release):
        raise ValueError('invalid release ID')
    from datetime import datetime
    datetime.fromisoformat(published.replace('Z', '+00:00'))
    output = pathlib.Path(output)
    output.mkdir(parents=True, exist_ok=True)
    destination = output / 'releases' / release
    if destination.exists():
        raise ValueError('release already exists; never replace immutable releases')
    with tempfile.TemporaryDirectory(dir=output) as temp:
        stage = pathlib.Path(temp)
        db = sqlite3.connect(stage / 'build.sqlite')
        db.executescript(pathlib.Path(seed).read_text())
        validate(db)
        db.execute(f'PRAGMA user_version={SCHEMA_VERSION}')
        db.execute('CREATE TABLE release_metadata(release TEXT NOT NULL, schema_version INTEGER NOT NULL, published_at TEXT NOT NULL)')
        db.execute('INSERT INTO release_metadata VALUES(?,?,?)', (release, SCHEMA_VERSION, published))
        db.execute("CREATE VIEW resources AS SELECT 'ks-' || printf('%06d',benefit_id) AS id, * FROM expanded_catalog")
        db.commit()
        snapshot = stage / 'catalog.sqlite'
        db.execute('VACUUM INTO ?', (str(snapshot),))
        db.close()
        with sqlite3.connect(snapshot) as check:
            validate(check)
        raw = snapshot.read_bytes()
        manifest = dict(release=release, schema_version=SCHEMA_VERSION, published_at=published,
                        url=f'/v1/catalog/releases/{release}/catalog.sqlite', byte_size=len(raw), sha256=hashlib.sha256(raw).hexdigest())
        destination.parent.mkdir(exist_ok=True)
        destination.mkdir()
        snapshot.rename(destination / 'catalog.sqlite')
        text = json.dumps(manifest, indent=2)+'\n'
        (destination / 'manifest.json').write_text(text)
        (stage / 'manifest.json').write_text(text)
        (stage / 'manifest.json').replace(output / 'manifest.json')
    return manifest

if __name__ == '__main__':
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--seed', default='seed/catalog.sql')
    p.add_argument('--output', default='catalog')
    p.add_argument('--release', required=True)
    p.add_argument('--published-at', required=True, help='ISO 8601 UTC timestamp')
    a=p.parse_args()
    print(json.dumps(publish(a.seed,a.output,a.release,a.published_at),indent=2))
