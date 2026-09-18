package consolejobs

// Cancellation persistence and validation live in action_control.go. These
// shared names keep supervisor failures bound to the existing durable barrier;
// they do not introduce another cancellation implementation or recovery flag.
const ActionContainmentCode = "execution_containment_lost"

var ErrActionContainment = ErrActionExecutionBlocked
