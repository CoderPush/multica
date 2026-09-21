#!/usr/bin/env python3
"""Bounded Web Lead Lark outbox. No database writes or agent-held bot secrets."""
import argparse
import base64
import datetime as dt
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import sqlite3
import subprocess
import urllib.error
import urllib.request

WORKSPACE = '7aec4dc4-be92-4dab-9eb9-2ab405af1e7c'
PROJECT = '1f065765-5630-4e67-9f16-adffaa6b3ce3'
LEAD = '23ac88c6-c40c-4800-ace7-fb22ed69f7bb'
CHAT = 'oc_8e7673e4309f0d2affdfd1f9e270f11f'
APP = 'cli_aa2275857cf89ed3'
MARKER = '<!-- web-lead-lark-update:v1 -->\n'
API = 'https://open.larksuite.com/open-apis'
MILESTONES = {'started', 'preview', 'blocked', 'recovered', 'shipped', 'decision'}
UTC = dt.timezone.utc


class MissingLarkSecretConfiguration(RuntimeError):
    """Safe deployment diagnostic, including in error-type-only sweep logs."""


def now():
    return dt.datetime.now(UTC).isoformat()


def command(args):
    result = subprocess.run(args, capture_output=True, text=True, timeout=30)
    if result.returncode:
        raise RuntimeError('local command failed: ' + Path(args[0]).name)
    return result.stdout.strip()


def query(sql):
    raw = command(['docker', 'exec', 'multica-postgres-1', 'psql', '-X', '-U',
                   'multica', '-d', 'multica', '-At', '-c', sql])
    return [json.loads(line) for line in raw.splitlines() if line]


def validate(row):
    if row['workspace_id'] != WORKSPACE or row['project_id'] != PROJECT:
        raise ValueError('wrong workspace or project')
    if row['author_type'] != 'agent' or row['author_id'] != LEAD:
        raise ValueError('not authored by Web Lead')
    if not row['content'].startswith(MARKER):
        raise ValueError('not an outbox request')
    data = json.loads(row['content'][len(MARKER):])
    if set(data) != {'reply_to', 'milestone', 'body', 'expected_status'}:
        raise ValueError('invalid request fields')
    if not isinstance(data['reply_to'], str) or not re.fullmatch(r'om_[A-Za-z0-9_]{8,100}', data['reply_to']):
        raise ValueError('invalid origin message')
    if data['milestone'] not in MILESTONES:
        raise ValueError('invalid milestone')
    if not isinstance(data['body'], str) or not 1 <= len(data['body'].strip()) <= 2400:
        raise ValueError('invalid body length')
    if '<at' in data['body'] or '![' in data['body']:
        raise ValueError('mentions and images are not supported')
    if data['expected_status'] != row['status']:
        raise ValueError('issue status changed; prepare a fresh update')
    age = dt.datetime.now(UTC) - dt.datetime.fromisoformat(row['created_at'].replace('Z', '+00:00'))
    if not 0 <= age.total_seconds() <= 1800:
        raise ValueError('request expired')
    digest = hashlib.sha256(json.dumps([row['issue_id'], data['reply_to'],
        data['milestone'], data['body']], ensure_ascii=False).encode()).hexdigest()
    return data, digest


class Lark:
    def __init__(self):
        from cryptography.hazmat.primitives.ciphers.aead import AESGCM
        rows = query(f"SELECT json_build_object('config', config) FROM channel_installation WHERE workspace_id='{WORKSPACE}' AND agent_id='{LEAD}' AND channel_type='feishu' AND status='active'")
        if len(rows) != 1:
            raise RuntimeError('expected one active Web Lead installation')
        config = rows[0]['config']
        if config['app_id'] != APP or config.get('region', 'lark') not in ('lark', 'international'):
            raise RuntimeError('unexpected Lark installation')
        # Docker output stays in memory; never print environment or credentials.
        env = json.loads(command(['docker', 'inspect', 'multica-backend-1', '--format', '{{json .Config.Env}}']))
        values = dict(entry.split('=', 1) for entry in env)
        if not values.get('MULTICA_LARK_SECRET_KEY'):
            raise MissingLarkSecretConfiguration('Missing deployment secret configuration: MULTICA_LARK_SECRET_KEY')
        key = base64.b64decode(values['MULTICA_LARK_SECRET_KEY'])
        sealed = base64.b64decode(config['app_secret_encrypted'])
        secret = AESGCM(key).decrypt(sealed[:12], sealed[12:], None).decode()
        token = self.request('/auth/v3/tenant_access_token/internal', {'app_id': APP, 'app_secret': secret})
        self.token = token['tenant_access_token']

    def request(self, path, body=None):
        headers = {'Content-Type': 'application/json'}
        if getattr(self, 'token', None):
            headers['Authorization'] = 'Bearer ' + self.token
        req = urllib.request.Request(API + path, data=json.dumps(body).encode() if body is not None else None, headers=headers)
        try:
            with urllib.request.urlopen(req, timeout=20) as response:
                result = json.load(response)
        except urllib.error.HTTPError as error:
            raise RuntimeError(f'Lark HTTP {error.code}') from None
        if result.get('code') != 0:
            raise RuntimeError(f'Lark API code {result.get("code")}')
        return result

    def check_origin(self, message_id):
        if not re.fullmatch(r'om_[A-Za-z0-9_]{8,100}', message_id):
            raise ValueError('invalid origin message')
        # Immutable ingress provenance avoids adding message-read permission.
        rows = query(f"""SELECT json_build_object('sender', d.channel_sender_id)
            FROM channel_task_delivery d JOIN channel_installation i ON i.id=d.installation_id
            WHERE d.channel_message_id='{message_id}' AND d.channel_chat_id='{CHAT}'
            AND d.channel_type='feishu' AND i.workspace_id='{WORKSPACE}'
            AND i.agent_id='{LEAD}' AND i.status='active'
            AND i.config->>'app_id'='{APP}'""")
        senders = {row.get('sender') for row in rows}
        if len(senders) != 1 or not re.fullmatch(r'ou_[A-Za-z0-9]+', next(iter(senders)) or ''):
            raise ValueError('origin has no matching immutable user ingress record')

    def reply(self, data, digest):
        content = {'en_us': {'content': [[{'tag': 'md', 'text': data['body']}]]}}
        result = self.request('/im/v1/messages/' + data['reply_to'] + '/reply', {
            'msg_type': 'post', 'content': json.dumps(content), 'reply_in_thread': True,
            'uuid': digest[:40]})
        message = result['data']
        if message.get('chat_id') != CHAT or not message.get('message_id'):
            raise RuntimeError('unexpected delivery receipt')
        return message['message_id']


def receipt(issue_id, value):
    command(['runuser', '-u', 'multica-worker', '--', '/usr/local/bin/multica', '--profile', 'server',
             'issue', 'metadata', 'set', issue_id, '--key', 'lark_delivery', '--value',
             json.dumps(value), '--output', 'json'])


def process(row, db, client_factory=Lark, write_receipt=receipt):
    existing = db.execute('SELECT state, receipt, published FROM deliveries WHERE comment_id=?', (row['id'],)).fetchone()
    if existing:
        # A failed metadata write can retry; a send must never repeat.
        if existing[0] == 'sending':
            value = {'state': 'uncertain', 'request_id': row['id'], 'at': now(),
                     'reason': 'Sender interrupted; reconcile Lark before requeueing.'}
            db.execute('UPDATE deliveries SET state=?,receipt=? WHERE comment_id=?', ('uncertain', json.dumps(value), row['id']))
            db.commit()
            existing = ('uncertain', json.dumps(value), 0)
        if existing[1] and not existing[2]:
            write_receipt(row['issue_id'], json.loads(existing[1]))
            db.execute('UPDATE deliveries SET published=1 WHERE comment_id=?', (row['id'],))
            db.commit()
        return existing[0]
    try:
        data, digest = validate(row)
    except (ValueError, TypeError, KeyError) as error:
        value = {'state': 'rejected', 'request_id': row['id'], 'reason': str(error), 'at': now()}
        db.execute('INSERT INTO deliveries(comment_id,issue_id,digest,state,receipt,created_at) VALUES(?,?,?,?,?,?)', (row['id'], row['issue_id'], None, 'rejected', json.dumps(value), now()))
        db.commit()
        write_receipt(row['issue_id'], value)
        db.execute('UPDATE deliveries SET published=1 WHERE comment_id=?', (row['id'],))
        db.commit()
        return 'rejected'
    duplicate = db.execute('SELECT state, receipt FROM deliveries WHERE digest=?', (digest,)).fetchone()
    if duplicate:
        value = {'state': 'duplicate', 'request_id': row['id'], 'original': json.loads(duplicate[1]) if duplicate[1] else {'state': duplicate[0]}, 'at': now()}
        db.execute('INSERT INTO deliveries(comment_id,issue_id,digest,state,receipt,created_at) VALUES(?,?,?,?,?,?)', (row['id'], row['issue_id'], None, 'duplicate', json.dumps(value), now()))
        db.commit()
        write_receipt(row['issue_id'], value)
        db.execute('UPDATE deliveries SET published=1 WHERE comment_id=?', (row['id'],))
        db.commit()
        return 'duplicate'
    # Preflight reads may fail safely. No send has been attempted yet.
    client = client_factory()
    try:
        client.check_origin(data['reply_to'])
    except ValueError as error:
        value = {'state': 'rejected', 'request_id': row['id'], 'reason': str(error), 'at': now()}
        db.execute('INSERT INTO deliveries(comment_id,issue_id,digest,state,receipt,created_at) VALUES(?,?,?,?,?,?)',
                   (row['id'], row['issue_id'], digest, 'rejected', json.dumps(value), now()))
        db.commit()
        return process(row, db, client_factory, write_receipt)
    db.execute('INSERT INTO deliveries(comment_id,issue_id,digest,state,receipt,created_at) VALUES(?,?,?,?,?,?)', (row['id'], row['issue_id'], digest, 'sending', None, now()))
    db.commit()
    try:
        message_id = client.reply(data, digest)
        value = {'state': 'sent', 'request_id': row['id'], 'message_id': message_id,
                 'reply_to': data['reply_to'], 'chat_id': CHAT, 'digest': digest, 'at': now()}
    except Exception:
        # Includes ambiguous timeouts. Reconcile with Lark before an operator retries.
        value = {'state': 'uncertain', 'request_id': row['id'], 'reply_to': data['reply_to'],
                 'digest': digest, 'at': now(), 'reason': 'Delivery uncertain; operator reconciliation required. Do not requeue.'}
    db.execute('UPDATE deliveries SET state=?,receipt=? WHERE comment_id=?', (value['state'], json.dumps(value), row['id']))
    db.commit()
    write_receipt(row['issue_id'], value)
    db.execute('UPDATE deliveries SET published=1 WHERE comment_id=?', (row['id'],))
    db.commit()
    return value['state']


def open_db(path):
    db = sqlite3.connect(path)
    db.execute('CREATE TABLE IF NOT EXISTS deliveries(comment_id TEXT PRIMARY KEY,issue_id TEXT NOT NULL,digest TEXT UNIQUE,state TEXT NOT NULL,receipt TEXT,created_at TEXT NOT NULL,published INTEGER NOT NULL DEFAULT 0)')
    return db


def sweep(rows, db, client_factory=Lark, write_receipt=receipt):
    errors = []
    # Drain receipts from durable state even when their source falls out of the
    # query window or a newer request supersedes it. Never repeat a send.
    pending = db.execute("SELECT comment_id,issue_id FROM deliveries WHERE state='sending' OR published=0 ORDER BY created_at").fetchall()
    blocked = set()
    for comment_id, issue_id in pending:
        try:
            process({'id': comment_id, 'issue_id': issue_id}, db, client_factory, write_receipt)
        except Exception as error:
            blocked.add(issue_id)
            errors.append((comment_id, type(error).__name__))
    blocked.update(row[0] for row in db.execute("SELECT issue_id FROM deliveries WHERE state='uncertain'"))
    seen = set()
    for row in rows:
        if row['issue_id'] in seen or row['issue_id'] in blocked:
            continue
        seen.add(row['issue_id'])
        try:
            outcome = process(row, db, client_factory, write_receipt)
            print(json.dumps({'request_id': row['id'], 'issue_id': row['issue_id'], 'state': outcome}))
        except Exception as error:
            errors.append((row['id'], type(error).__name__))
    return errors


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--state-dir', default='/var/lib/multica-web-lead-delivery')
    parser.add_argument('--since', required=True, help='Activation UTC timestamp; no historical sends')
    parser.add_argument('--dry-run', action='store_true')
    args = parser.parse_args()
    activation = dt.datetime.fromisoformat(args.since.replace('Z', '+00:00')).isoformat()
    state = Path(args.state_dir)
    state.mkdir(mode=0o700, parents=True, exist_ok=True)
    with (state / 'lock').open('w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        rows = query(f"""SELECT json_build_object('id',c.id,'issue_id',i.id,'workspace_id',i.workspace_id,
        'project_id',i.project_id,'status',i.status,'author_id',c.author_id,'author_type',c.author_type,
        'content',c.content,'created_at',c.created_at) FROM comment c JOIN issue i ON i.id=c.issue_id
        WHERE c.workspace_id='{WORKSPACE}' AND i.workspace_id='{WORKSPACE}' AND i.project_id='{PROJECT}'
        AND c.author_type='agent' AND c.author_id='{LEAD}' AND c.content LIKE '<!-- web-lead-lark-update:v1 -->%'
        AND c.created_at >= '{activation}'::timestamptz ORDER BY c.created_at DESC LIMIT 50""")
        db = open_db(state / 'receipts.sqlite3')
        if args.dry_run:
            for row in rows:
                validate(row)
                print(json.dumps({'request_id': row['id'], 'state': 'valid-unsent'}))
            return
        errors = sweep(rows, db)
        for request_id, error_type in errors:
            print(json.dumps({'request_id': request_id, 'state': 'failed', 'error_type': error_type}))
        if errors:
            raise SystemExit(1)


if __name__ == '__main__':
    main()
