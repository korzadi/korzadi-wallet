package compliance

// allowedTransitions is the ONLY definition of legal KYC transitions; every
// writer (webhook, admin action, expiration worker) goes through
// ValidateTransition so no path can skip it.
//
//	NOT_STARTED -> PENDING
//	PENDING     -> IN_REVIEW | VERIFIED | REJECTED | EXPIRED | SUSPENDED
//	IN_REVIEW   -> PENDING | VERIFIED | REJECTED | SUSPENDED
//	VERIFIED    -> EXPIRED | SUSPENDED
//	SUSPENDED   -> IN_REVIEW | REJECTED
//	REJECTED, EXPIRED: terminal (a new case is created to re-verify)
var allowedTransitions = map[KYCStatus]map[KYCStatus]bool{
	KYCNotStarted: {KYCPending: true},
	KYCPending:    {KYCInReview: true, KYCVerified: true, KYCRejected: true, KYCExpired: true, KYCSuspended: true},
	KYCInReview:   {KYCPending: true, KYCVerified: true, KYCRejected: true, KYCSuspended: true},
	KYCVerified:   {KYCExpired: true, KYCSuspended: true},
	KYCSuspended:  {KYCInReview: true, KYCRejected: true},
	KYCRejected:   {},
	KYCExpired:    {},
}

// ValidateTransition returns ErrInvalidTransition unless from -> to is legal.
func ValidateTransition(from, to KYCStatus) error {
	if !from.Valid() || !to.Valid() {
		return ErrInvalidTransition
	}
	if !allowedTransitions[from][to] {
		return ErrInvalidTransition
	}
	return nil
}

// CanTransition is the boolean form of ValidateTransition.
func CanTransition(from, to KYCStatus) bool { return ValidateTransition(from, to) == nil }

// adminAllowed restricts what a HUMAN may do (spec §13): reviewers move a
// case to IN_REVIEW; admins decide. The provider path is not restricted
// beyond allowedTransitions.
var adminAllowed = map[KYCStatus]map[KYCStatus]bool{
	KYCPending:   {KYCInReview: true},
	KYCInReview:  {KYCVerified: true, KYCRejected: true, KYCSuspended: true, KYCPending: true},
	KYCVerified:  {KYCSuspended: true},
	KYCSuspended: {KYCInReview: true, KYCRejected: true},
}

func validateAdminTransition(from, to KYCStatus) error {
	if err := ValidateTransition(from, to); err != nil {
		return err
	}
	if !adminAllowed[from][to] {
		return ErrInvalidTransition
	}
	return nil
}
