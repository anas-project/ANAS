package incusingresshost

import (
	"encoding/json"
	"fmt"
)

func forwardingBridgePortsAbsent(body []byte) error {
	var ports []json.RawMessage
	if decodeObservedJSON(body, &ports) != nil || ports == nil || len(ports) != 0 {
		return fmt.Errorf("forwarding bridge still has ports or its inventory is incomplete")
	}
	return nil
}
