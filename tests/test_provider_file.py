import json
import os

import pytest

from orchestrator.config.provider_file import apply_provider_file, read_provider_file


def config(model='first'):
    return {'ANTHROPIC_AUTH_TOKEN': 'private-test-value', 'ANTHROPIC_MODEL': model}


def test_mixed_legacy_notes_select_complete_last_profile(tmp_path):
    path = tmp_path / 'notes.txt'
    path.write_text(json.dumps(config())[1:-1] + ',\nnotes:\n' + json.dumps({'env': config('second'), 'enabledPlugins': {'untrusted': True}}), encoding='utf-8-sig')
    assert read_provider_file(path)['ANTHROPIC_MODEL'] == 'second'
    assert read_provider_file(path, '1')['ANTHROPIC_MODEL'] == 'first'
    assert 'enabledPlugins' not in read_provider_file(path)


def test_profiles_replace_stale_credentials(tmp_path, monkeypatch):
    path = tmp_path / 'profiles.json'
    path.write_text(json.dumps({'version': 1, 'active_profile': 'demo', 'profiles': {'demo': {'env': config()}}}))
    monkeypatch.setattr(os, 'environ', dict(os.environ, ANTHROPIC_API_KEY='stale', LLM_PROVIDER='openai', MODEL_FAST='wrong'))
    apply_provider_file(path)
    assert 'ANTHROPIC_API_KEY' not in os.environ
    assert os.environ['LLM_PROVIDER'] == 'anthropic'
    assert os.environ['MODEL_FAST'] == ''
    with pytest.raises(ValueError, match='profile not found'):
        read_provider_file(path, 'missing')


def test_validation_failure_leaves_environment_unchanged(tmp_path, monkeypatch):
    path = tmp_path / 'bad.json'
    path.write_text(json.dumps({'OPENAI_MODEL': 'model-without-key'}))
    monkeypatch.setattr(os, 'environ', {'LLM_PROVIDER': 'original'})
    with pytest.raises(ValueError, match='credential is required'):
        apply_provider_file(path)
    assert os.environ == {'LLM_PROVIDER': 'original'}


def test_openai_profile_preserves_supported_options(tmp_path):
    path = tmp_path / 'openai.json'
    path.write_text(json.dumps({'model': 'custom-model', 'env': {
        'OPENAI_API_KEY': 'private-test-value', 'OPENAI_TIMEOUT': 30,
        'OPENAI_MAX_RETRIES': 2, 'THINKING_ENABLED': True,
    }}))
    result = read_provider_file(path)
    assert result['LLM_PROVIDER'] == 'openai'
    assert result['OPENAI_MODEL'] == 'custom-model'
    assert result['OPENAI_TIMEOUT'] == '30'
    assert result['THINKING_ENABLED'] == 'true'


@pytest.mark.parametrize('field,value', [('ANTHROPIC_TIMEOUT', -1), ('ANTHROPIC_TIMEOUT', 'nan'), ('ANTHROPIC_MAX_RETRIES', 1.5), ('ANTHROPIC_BASE_URL', 'file:///secret'), ('THINKING_ENABLED', 'maybe'), ('ANTHROPIC_MODEL', {})])
def test_invalid_fields_do_not_expose_values(tmp_path, field, value):
    path = tmp_path / 'bad.json'
    path.write_text(json.dumps(dict(config(), **{field: value})))
    with pytest.raises(ValueError) as caught:
        read_provider_file(path)
    assert 'private-test-value' not in str(caught.value)
