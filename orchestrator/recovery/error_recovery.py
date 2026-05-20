from __future__ import annotations

from enum import Enum


class RecoveryStrategy(str, Enum):
    RETRY_SAME = "retry_same"
    SWITCH_MODEL = "switch_model"
    ASK_USER = "ask_user"
    ABORT = "abort"


class ErrorRecoveryEngine:
    def choose(self, error_count: int, last_error: str = "") -> RecoveryStrategy:
        message = last_error.lower()
        if "permission" in message:
            return RecoveryStrategy.ASK_USER
        if error_count >= 2:
            return RecoveryStrategy.SWITCH_MODEL
        if error_count == 1:
            return RecoveryStrategy.RETRY_SAME
        return RecoveryStrategy.ABORT
