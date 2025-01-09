package prometheus

import "time"

func sync() {

}

func SyncPeriodic(intervalSeconds int) {
	for {
		sync()
		time.Sleep(time.Duration(intervalSeconds) * time.Second)
	}
}
