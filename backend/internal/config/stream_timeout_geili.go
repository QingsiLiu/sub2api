package config

import "fmt"

func validateLongThinkingStreamTimeout(seconds int) error {
	if seconds != 0 && (seconds < 30 || seconds > 1800) {
		return fmt.Errorf("gateway.long_thinking_stream_data_interval_timeout must be 0 or between 30-1800 seconds")
	}
	return nil
}
