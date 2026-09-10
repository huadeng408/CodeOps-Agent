"""Load replaceable provider profiles without interpreting foreign app settings."""
from __future__ import annotations

import argparse
import json
import math
import os
from pathlib import Path
from urllib.parse import urlsplit

FIELDS = {
    'LLM_PROVIDER', 'MODEL_FAST', 'THINKING_ENABLED',
    'OPENAI_API_KEY', 'OPENAI_BASE_URL', 'OPENAI_MODEL', 'OPENAI_TIMEOUT',
    'OPENAI_MAX_RETRIES', 'OPENAI_MAX_TOKENS',
    'ANTHROPIC_API_KEY', 'ANTHROPIC_AUTH_TOKEN', 'ANTHROPIC_BASE_URL',
    'ANTHROPIC_MODEL', 'ANTHROPIC_TIMEOUT', 'ANTHROPIC_MAX_RETRIES',
    'ANTHROPIC_MAX_TOKENS', 'ANTHROPIC_THINKING_BUDGET_TOKENS',
    'HTTP_PROXY', 'HTTPS_PROXY', 'NO_PROXY',
}


def _documents(text: str) -> list[dict]:
    decoder = json.JSONDecoder()
    try:
        value = json.loads(text)
        if not isinstance(value, dict):
            raise ValueError('Provider configuration must be an object')
        return [value]
    except json.JSONDecodeError:
        pass
    # Legacy notes contain a bare field fragment followed by prose and JSON.
    documents = []
    fragment = {}
    offset = 0
    while offset < len(text):
        char = text[offset]
        if char not in '{"':
            offset += 1
            continue
        try:
            value, end = decoder.raw_decode(text, offset)
        except json.JSONDecodeError:
            offset += 1
            continue
        if isinstance(value, dict):
            if fragment:
                documents.append(fragment)
                fragment = {}
            documents.append(value)
        elif isinstance(value, str):
            colon = end
            while colon < len(text) and text[colon].isspace():
                colon += 1
            if colon < len(text) and text[colon] == ':':
                start = colon + 1
                while start < len(text) and text[start].isspace():
                    start += 1
                try:
                    field, end = decoder.raw_decode(text, start)
                    fragment[value] = field
                except json.JSONDecodeError:
                    raise ValueError('Invalid provider field value') from None
        offset = end
    if fragment:
        documents.append(fragment)
    if not documents:
        raise ValueError('No provider configuration found')
    return documents


def read_provider_file(path: str | Path, profile: str = '') -> dict[str, str]:
    try:
        text = Path(path).read_text(encoding='utf-8-sig')
    except (OSError, UnicodeError):
        raise ValueError('Provider configuration file cannot be read') from None
    documents = _documents(text)
    if len(documents) == 1 and 'profiles' in documents[0]:
        root = documents[0]
        profiles = root['profiles']
        if root.get('version', 1) != 1 or not isinstance(profiles, dict) or not profiles:
            raise ValueError('Invalid provider profile schema')
        selected = profile or root.get('active_profile', '')
        if selected not in profiles:
            raise ValueError('Provider profile not found')
        config = profiles[selected]
    else:
        try:
            index = int(profile) - 1 if profile else len(documents) - 1
            if index < 0:
                raise ValueError()
            config = documents[index]
        except (ValueError, IndexError):
            raise ValueError('Legacy profile must be a valid 1-based index') from None
    if not isinstance(config, dict):
        raise ValueError('Provider profile must be an object')
    values = config.get('env', config)
    if not isinstance(values, dict):
        raise ValueError('Provider env must be an object')
    result = {}
    for key in FIELDS & values.keys():
        value = values[key]
        if not isinstance(value, (str, int, float, bool)):
            raise ValueError('Provider field must be a scalar: ' + key)
        result[key] = str(value).lower() if isinstance(value, bool) else str(value).strip()
        if '\x00' in result[key] or '\n' in result[key] or '\r' in result[key]:
            raise ValueError('Invalid provider field: ' + key)
    provider = result.get('LLM_PROVIDER') or ('anthropic' if any(result.get(k) for k in ('ANTHROPIC_API_KEY', 'ANTHROPIC_AUTH_TOKEN')) else 'openai')
    if provider not in {'anthropic', 'openai'}:
        raise ValueError('Unsupported provider protocol')
    prefix = provider.upper()
    if not result.get(prefix + '_MODEL') and isinstance(config.get('model'), str):
        result[prefix + '_MODEL'] = config['model']
    if not result.get(prefix + '_MODEL'):
        raise ValueError('Provider model is required')
    if not result.get(prefix + '_API_KEY') and not (provider == 'anthropic' and result.get('ANTHROPIC_AUTH_TOKEN')):
        raise ValueError('Provider credential is required')
    for key, value in result.items():
        if key.endswith('_BASE_URL') or key in {'HTTP_PROXY', 'HTTPS_PROXY'}:
            parsed = urlsplit(value)
            if parsed.scheme not in {'http', 'https'} or not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment:
                raise ValueError('Invalid provider URL: ' + key)
        if key.endswith(('_TIMEOUT', '_MAX_RETRIES', '_MAX_TOKENS', '_BUDGET_TOKENS')):
            try:
                number = float(value)
                if not math.isfinite(number) or number < 0 or (key.endswith('_TIMEOUT') and number == 0):
                    raise ValueError()
                if not key.endswith('_TIMEOUT') and not number.is_integer():
                    raise ValueError()
            except ValueError:
                raise ValueError('Invalid numeric provider field: ' + key) from None
        if key == 'THINKING_ENABLED' and value not in {'true', 'false'}:
            raise ValueError('THINKING_ENABLED must be true or false')
    result['LLM_PROVIDER'] = provider
    # An explicit file replaces stale provider env, including implicit fast routing.
    result.setdefault('MODEL_FAST', '')
    result.setdefault('THINKING_ENABLED', 'false')
    result.setdefault('NO_PROXY', 'localhost,127.0.0.1,::1')
    return result


def apply_provider_file(path: str | Path, profile: str = '') -> None:
    values = read_provider_file(path, profile)
    for key in FIELDS - {'HTTP_PROXY', 'HTTPS_PROXY', 'NO_PROXY'}:
        os.environ.pop(key, None)
    os.environ.update(values)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument('path')
    parser.add_argument('--profile', default='')
    args = parser.parse_args()
    try:
        values = read_provider_file(args.path, args.profile)
    except ValueError as exc:
        parser.exit(1, str(exc) + '\n')
    print(json.dumps({'valid': True, 'provider': values['LLM_PROVIDER'], 'fields': sorted(values)}))


if __name__ == '__main__':
    main()
