package runtime

func (coordinator *Coordinator) workerReturned(returned *request) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()

	if returned.lease == nil || coordinator.active == 0 {
		panic("runtime: returned worker does not own an active slot")
	}
	returned.state = Returned
	returned.cancel()
	delete(coordinator.requests, string(returned.idKey))
	coordinator.active--
	coordinator.promoteHeadLocked()
}

func (coordinator *Coordinator) promoteHeadLocked() {
	head := coordinator.queue.Front()
	if head == nil || coordinator.active >= coordinator.limits.MaxConcurrent {
		return
	}

	promoted := head.Value.(*request)
	coordinator.queue.Remove(head)
	promoted.queueNode = nil
	coordinator.stopQueueTimerLocked(promoted)
	promoted.state = Running
	coordinator.active++
	coordinator.signalReadyLocked(promoted)
}

func (coordinator *Coordinator) cancel(cancelled *request) {
	var noCommit *WorkLease
	coordinator.mu.Lock()

	switch cancelled.state {
	case Queued:
		if cancelled.queueNode != nil {
			coordinator.queue.Remove(cancelled.queueNode)
			cancelled.queueNode = nil
		}
		coordinator.stopQueueTimerLocked(cancelled)
		cancelled.state = Cancelled
		cancelled.cancel()
		delete(coordinator.requests, string(cancelled.idKey))
		coordinator.signalReadyLocked(cancelled)
	case Running:
		cancelled.cancel()
		if cancelled.lease == nil {
			cancelled.state = Cancelled
			delete(coordinator.requests, string(cancelled.idKey))
			coordinator.active--
			coordinator.signalReadyLocked(cancelled)
			coordinator.promoteHeadLocked()
		} else {
			cancelled.state = Lingering
			noCommit = cancelled.lease
		}
	case Cancelled, TimeoutResponseCommitted, ResponseCommitted, Lingering, Returned:
		coordinator.mu.Unlock()
		return
	}
	coordinator.mu.Unlock()
	if noCommit != nil {
		noCommit.MarkNoCommit()
	}
}

func (coordinator *Coordinator) queueTimedOut(expired *request) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()

	if expired.state != Queued {
		return
	}
	if expired.queueNode != nil {
		coordinator.queue.Remove(expired.queueNode)
		expired.queueNode = nil
	}
	expired.timer = nil
	expired.state = TimeoutResponseCommitted
	expired.response = &ResponseRight{queueTimeout: true}
	expired.cancel()
	delete(coordinator.requests, string(expired.idKey))
	coordinator.signalReadyLocked(expired)
}

func (coordinator *Coordinator) failQueueTimerAfterPanic(failed *request) {
	var noCommit *WorkLease
	func() {
		coordinator.mu.Lock()
		defer coordinator.mu.Unlock()

		if failed.queueNode != nil {
			coordinator.queue.Remove(failed.queueNode)
			failed.queueNode = nil
		}
		failed.timer = nil
		failed.response = nil
		if failed.cancel != nil {
			failed.cancel()
		}

		switch failed.state {
		case Running, Lingering:
			if failed.lease != nil {
				failed.state = Lingering
				noCommit = failed.lease
				break
			}
			failed.state = Cancelled
			delete(coordinator.requests, string(failed.idKey))
			if coordinator.active != 0 {
				coordinator.active--
			}
			coordinator.signalReadyLocked(failed)
			coordinator.promoteHeadLocked()
		case Returned, Cancelled:
			delete(coordinator.requests, string(failed.idKey))
			coordinator.signalReadyLocked(failed)
		default:
			failed.state = Cancelled
			delete(coordinator.requests, string(failed.idKey))
			coordinator.signalReadyLocked(failed)
		}
	}()

	if noCommit != nil {
		noCommit.MarkNoCommit()
	}
}

func (coordinator *Coordinator) stopQueueTimerLocked(request *request) {
	if request.timer == nil {
		return
	}
	request.timer.Stop()
	request.timer = nil
}

func (coordinator *Coordinator) signalReadyLocked(request *request) {
	if request.readyClosed {
		return
	}
	request.readyClosed = true
	close(request.ready)
}
