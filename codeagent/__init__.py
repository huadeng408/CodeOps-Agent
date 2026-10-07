"""Generated Python protobuf package for code-agent."""

# Field 4 is canonically P2 while older Python callers may still construct
# ``ContextEnvelope(p3_candidates=...)``. This adapter keeps one wire field and
# one fact source; it is intentionally removed after all callers migrate.
from . import orchestrator_pb2 as _orchestrator_pb2

_context_envelope_init = _orchestrator_pb2.ContextEnvelope.__init__


def _context_envelope_compat_init(self, *args, **kwargs):
    legacy = kwargs.pop("p3_candidates", None)
    if legacy is not None:
        if "p2_candidates" in kwargs:
            raise TypeError("ContextEnvelope accepts p2_candidates or p3_candidates, not both")
        kwargs["p2_candidates"] = legacy
    _context_envelope_init(self, *args, **kwargs)


_orchestrator_pb2.ContextEnvelope.__init__ = _context_envelope_compat_init
_orchestrator_pb2.ContextEnvelope.p3_candidates = property(
    lambda self: self.p2_candidates,
    lambda self, value: self.p2_candidates.__setitem__(slice(None), value),
)
