package dashboard

import "fmt"

func errUnknownMetric(id string) error { return fmt.Errorf("dashboard: no catalog metric %q", id) }
func errNilSeries(id string) error     { return fmt.Errorf("dashboard: nil series provider for %q", id) }
func errSeriesPanic(r any) error       { return fmt.Errorf("dashboard: series provider panicked: %v", r) }
