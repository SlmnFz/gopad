package metrics

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

var defaultBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5}

type histogram struct {
	buckets []float64
	counts  []uint64
	count   uint64
	sum     float64
}

func newHistogram() histogram {
	return histogram{buckets: append([]float64(nil), defaultBuckets...), counts: make([]uint64, len(defaultBuckets))}
}

func (histogram *histogram) observe(value float64) {
	histogram.count++
	histogram.sum += value
	for index, bucket := range histogram.buckets {
		if value <= bucket {
			histogram.counts[index]++
		}
	}
}

// Metrics is the process-wide observability state for one Gopad server.
// Values are exported directly in Prometheus text format to avoid a runtime
// dependency for this intentionally small service.
//
// Prometheus metric names are the public observability contract:
//
//   - gopad_active_connections (gauge)
//   - gopad_active_rooms (gauge)
//   - gopad_operations_total (counter)
//   - gopad_dropped_clients_total (counter)
//   - gopad_write_queue_dropped_total (counter)
//   - gopad_write_queue_depth (gauge)
//   - gopad_broadcast_latency_seconds (histogram)
//   - gopad_write_batch_size (histogram)
//   - gopad_write_batch_latency_seconds (histogram)
type Metrics struct {
	mu sync.Mutex

	activeConnections int64
	activeRooms       int64
	operations        uint64
	droppedClients    uint64
	queueDropped      uint64
	writeQueueDepth   int

	broadcastLatency  histogram
	writeBatchSize    histogram
	writeBatchLatency histogram
}

func New() *Metrics {
	return &Metrics{
		broadcastLatency:  newHistogram(),
		writeBatchSize:    newHistogram(),
		writeBatchLatency: newHistogram(),
	}
}

func (metrics *Metrics) SetActiveConnections(value int64) {
	if metrics == nil {
		return
	}
	metrics.mu.Lock()
	metrics.activeConnections = value
	metrics.mu.Unlock()
}

func (metrics *Metrics) IncActiveConnections() {
	metrics.AddActiveConnections(1)
}

func (metrics *Metrics) DecActiveConnections() {
	metrics.AddActiveConnections(-1)
}

func (metrics *Metrics) AddActiveConnections(delta int64) {
	if metrics == nil {
		return
	}
	metrics.mu.Lock()
	metrics.activeConnections += delta
	if metrics.activeConnections < 0 {
		metrics.activeConnections = 0
	}
	metrics.mu.Unlock()
}

func (metrics *Metrics) SetActiveRooms(value int64) {
	if metrics == nil {
		return
	}
	metrics.mu.Lock()
	metrics.activeRooms = value
	metrics.mu.Unlock()
}

func (metrics *Metrics) AddActiveRooms(delta int64) {
	if metrics == nil {
		return
	}
	metrics.mu.Lock()
	metrics.activeRooms += delta
	if metrics.activeRooms < 0 {
		metrics.activeRooms = 0
	}
	metrics.mu.Unlock()
}

func (metrics *Metrics) IncOperations() {
	if metrics == nil {
		return
	}
	metrics.mu.Lock()
	metrics.operations++
	metrics.mu.Unlock()
}

func (metrics *Metrics) ObserveBroadcastLatency(duration time.Duration) {
	if metrics == nil {
		return
	}
	metrics.mu.Lock()
	metrics.broadcastLatency.observe(duration.Seconds())
	metrics.mu.Unlock()
}

func (metrics *Metrics) ObserveWriteBatch(size int, duration time.Duration) {
	if metrics == nil {
		return
	}
	metrics.mu.Lock()
	metrics.writeBatchSize.observe(float64(size))
	metrics.writeBatchLatency.observe(duration.Seconds())
	metrics.mu.Unlock()
}

func (metrics *Metrics) SetWriteQueueDepth(value int) {
	if metrics == nil {
		return
	}
	metrics.mu.Lock()
	metrics.writeQueueDepth = value
	metrics.mu.Unlock()
}

func (metrics *Metrics) IncWriteQueueDropped() {
	if metrics == nil {
		return
	}
	metrics.mu.Lock()
	metrics.queueDropped++
	metrics.mu.Unlock()
}

func (metrics *Metrics) IncDroppedClients() {
	if metrics == nil {
		return
	}
	metrics.mu.Lock()
	metrics.droppedClients++
	metrics.mu.Unlock()
}

// ServeHTTP exposes the metrics in the Prometheus text exposition format.
func (metrics *Metrics) ServeHTTP(response http.ResponseWriter, _ *http.Request) {
	if metrics == nil {
		http.Error(response, "metrics are not configured", http.StatusServiceUnavailable)
		return
	}
	metrics.mu.Lock()
	var output strings.Builder
	fmt.Fprintf(&output, "# TYPE gopad_active_connections gauge\ngopad_active_connections %d\n", metrics.activeConnections)
	fmt.Fprintf(&output, "# TYPE gopad_active_rooms gauge\ngopad_active_rooms %d\n", metrics.activeRooms)
	fmt.Fprintf(&output, "# TYPE gopad_operations_total counter\ngopad_operations_total %d\n", metrics.operations)
	fmt.Fprintf(&output, "# TYPE gopad_dropped_clients_total counter\ngopad_dropped_clients_total %d\n", metrics.droppedClients)
	fmt.Fprintf(&output, "# TYPE gopad_write_queue_dropped_total counter\ngopad_write_queue_dropped_total %d\n", metrics.queueDropped)
	fmt.Fprintf(&output, "# TYPE gopad_write_queue_depth gauge\ngopad_write_queue_depth %d\n", metrics.writeQueueDepth)
	writeHistogram(&output, "gopad_broadcast_latency_seconds", &metrics.broadcastLatency)
	writeHistogram(&output, "gopad_write_batch_size", &metrics.writeBatchSize)
	writeHistogram(&output, "gopad_write_batch_latency_seconds", &metrics.writeBatchLatency)
	metrics.mu.Unlock()

	response.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = response.Write([]byte(output.String()))
}

func writeHistogram(output *strings.Builder, name string, histogram *histogram) {
	fmt.Fprintf(output, "# TYPE %s histogram\n", name)
	for index, bucket := range histogram.buckets {
		fmt.Fprintf(output, "%s_bucket{le=\"%g\"} %d\n", name, bucket, histogram.counts[index])
	}
	fmt.Fprintf(output, "%s_bucket{le=\"+Inf\"} %d\n", name, histogram.count)
	fmt.Fprintf(output, "%s_sum %g\n%s_count %d\n", name, histogram.sum, name, histogram.count)
}
