package organizationdomain

const (
	VerificationStatusUnverified = "unverified"
	VerificationStatusPending    = "pending"
	VerificationStatusVerified   = "verified"
	VerificationStatusRejected   = "rejected"

	// TrustStatusUnassessed is intentionally the only trust state until a
	// BridgeWorks trust/fraud policy defines additional states and transitions.
	TrustStatusUnassessed = "unassessed"
)

func CanTransitionVerification(from, to string) bool {
	if from == to {
		return isVerificationStatus(from)
	}

	switch from {
	case VerificationStatusUnverified:
		return to == VerificationStatusPending
	case VerificationStatusPending:
		return to == VerificationStatusVerified || to == VerificationStatusRejected
	case VerificationStatusRejected:
		return to == VerificationStatusPending
	case VerificationStatusVerified:
		return false
	default:
		return false
	}
}

func CanTransitionTrust(from, to string) bool {
	return from == TrustStatusUnassessed && to == TrustStatusUnassessed
}

func isVerificationStatus(status string) bool {
	switch status {
	case VerificationStatusUnverified,
		VerificationStatusPending,
		VerificationStatusVerified,
		VerificationStatusRejected:
		return true
	default:
		return false
	}
}
