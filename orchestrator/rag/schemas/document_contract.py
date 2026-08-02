"""Frozen document-level contract for the multimodal corpus (design spec §3.1).

Field names and validation MUST match the Go side
(internal/model/document_contract.go). Tenant/ACL fields are written only by
the Go control plane and are intentionally absent here.
"""

from __future__ import annotations

from pydantic import BaseModel, ConfigDict, field_validator


class DocumentContract(BaseModel):
    model_config = ConfigDict(extra="forbid")

    document_id: str
    source_id: str
    source_uri: str
    source_sha256: str
    mime: str = ""
    parser_name: str
    parser_version: str
    parser_backend: str = ""
    license_id: str = ""
    corpus_generation: str

    @field_validator(
        "document_id",
        "source_id",
        "source_uri",
        "source_sha256",
        "parser_name",
        "parser_version",
        "corpus_generation",
    )
    @classmethod
    def _reject_blank(cls, value: str) -> str:
        if not value.strip():
            raise ValueError("must not be blank")
        return value
