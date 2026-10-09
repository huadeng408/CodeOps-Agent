package session

// These identify the known verification stream shared with the admission
// module. Conversation readers must leave its projection to that module.
const VerificationAdmissionLedgerID = "admission:verification:v1"
const VerificationAdmissionCreatedEventType = "admission/batch_created"

func isVerificationAdmissionStream(events []Event) bool {
	return len(events) > 0 && events[0].SessionID == VerificationAdmissionLedgerID && events[0].Type == VerificationAdmissionCreatedEventType
}
