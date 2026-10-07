# Security Policy

## Supported versions

Security fixes target the `main` branch. This project is primarily a local
developer tool; deployments that expose its HTTP API to a network must add
their own TLS, authentication, network policy, and operational hardening.

## Reporting a vulnerability

Please do not open a public issue for a suspected vulnerability. Use the
repository owner's private GitHub security advisory flow when it is enabled,
or contact the repository owner through the GitHub profile associated with
`huadeng408/CodeOps-Agent`. Include the affected commit, reproduction steps,
impact, and a minimal proof of concept. Do not include real credentials or
personal data; redact them before sending.

We will acknowledge reports after triage and will keep the report private
while a fix or mitigation is prepared.

## Credential handling

Credentials must come from the process environment or an approved secret
manager. Never commit `.env` files, provider tokens, private keys, database
passwords, session databases, logs, or browser artifacts. Use `.env.example`
and `configs/provider.example.json` as templates only.
