package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"time"

	"github.com/docker/docker/client"
	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
)

var previousCPU = 0.0
var previousSystem = 0.0

func calculateCPUPercentUsage(v Stats) float64 {
	var (
		cpuPercent = 0.0
		// calculate the change for the cpu usage of the container in between readings
		cpuDelta = float64(v.CPUStats.CPUUsage.TotalUsage) - float64(previousCPU)
		// calculate the change for the entire system between readings
		systemDelta = float64(v.CPUStats.SystemUsage) - float64(previousSystem)
	)

	if systemDelta > 0.0 && cpuDelta > 0.0 {
		cpuPercent = (cpuDelta / systemDelta) * float64(v.CPUStats.OnlineCPUs) * 100.0
	}
	previousCPU = float64(v.CPUStats.CPUUsage.TotalUsage)
	previousSystem = float64(v.CPUStats.SystemUsage)
	return cpuPercent

}

var influxClient influxdb2.Client

func sendDataToInflux(id string, data float64) error {

	// Use blocking write client for writes to desired bucket
	writeAPI := influxClient.WriteAPIBlocking("org", "org")
	// Create point using full params constructor
	p := influxdb2.NewPoint("cpu_usage",
		map[string]string{"unit": "%", "id":id},
		map[string]interface{}{"cpu": data},
		time.Now())
	// write point immediately
	err := writeAPI.WritePoint(context.Background(), p)
	if err != nil {
		return err
	}
	return nil
}

func main() {
	influxClient = influxdb2.NewClient("http://localhost:8086", "1DY5ZHHn8I-ggzazl3BJMzgl8u-RkTmPbRxUbCYl1v-bj7UWlNlOhgfHqDZvJ8Zc779IzD8mfmczGBr4lNUwKQ==")
	jsonFile, err := os.Open("containers.json")

	// if we os.Open returns an error then handle it
	if err != nil {
		fmt.Println(err)
	}
	type Container struct {
		Name string `json:"name"`
	}
	type Containers struct {
		Containers []Container `json:"containers"`
	}

	byteValue, _ := ioutil.ReadAll(jsonFile)

	var containers Containers

	json.Unmarshal(byteValue, &containers)

	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		panic(err)
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	done := make(chan bool)

	for _, container := range containers.Containers {
		go func(container Container) {
			for {
				select {
				case <-done:
					return
				case t := <-ticker.C:
					fmt.Println(t)
					res, err := cli.ContainerStats(context.Background(), container.Name, false)
					if err == nil {
						buf := new(bytes.Buffer)

						_, err := buf.ReadFrom(res.Body)
						if err != nil {
							fmt.Println(err)
							os.Exit(1)
						}

						res.Body.Close()

						var stats Stats
						json.Unmarshal(buf.Bytes(), &stats)
						// fmt.Println(buf.String())
						// fmt.Println(stats)
						cpu_usage := calculateCPUPercentUsage(stats)
						// fmt.Println(container, calculateCPUPercentUsage(stats))
						err = sendDataToInflux(container.Name, cpu_usage)
						if err != nil {
							fmt.Println(err)
						}
					}
				}
			}
		}(container)
	}

	time.Sleep(1 * time.Hour)
	ticker.Stop()
	done <- true
	fmt.Println("Ticker stopped")

}

// ThrottlingData stores CPU throttling stats of one running container.
// Not used on Windows.
type ThrottlingData struct {
	// Number of periods with throttling active
	Periods uint64 `json:"periods"`
	// Number of periods when the container hits its throttling limit.
	ThrottledPeriods uint64 `json:"throttled_periods"`
	// Aggregate time the container was throttled for in nanoseconds.
	ThrottledTime uint64 `json:"throttled_time"`
}

// CPUUsage stores All CPU stats aggregated since container inception.
type CPUUsage struct {
	// Total CPU time consumed.
	// Units: nanoseconds (Linux)
	// Units: 100's of nanoseconds (Windows)
	TotalUsage uint64 `json:"total_usage"`

	// Total CPU time consumed per core (Linux). Not used on Windows.
	// Units: nanoseconds.
	PercpuUsage []uint64 `json:"percpu_usage,omitempty"`

	// Time spent by tasks of the cgroup in kernel mode (Linux).
	// Time spent by all container processes in kernel mode (Windows).
	// Units: nanoseconds (Linux).
	// Units: 100's of nanoseconds (Windows). Not populated for Hyper-V Containers.
	UsageInKernelmode uint64 `json:"usage_in_kernelmode"`

	// Time spent by tasks of the cgroup in user mode (Linux).
	// Time spent by all container processes in user mode (Windows).
	// Units: nanoseconds (Linux).
	// Units: 100's of nanoseconds (Windows). Not populated for Hyper-V Containers
	UsageInUsermode uint64 `json:"usage_in_usermode"`
}

// CPUStats aggregates and wraps all CPU related info of container
type CPUStats struct {
	// CPU Usage. Linux and Windows.
	CPUUsage CPUUsage `json:"cpu_usage"`

	// System Usage. Linux only.
	SystemUsage uint64 `json:"system_cpu_usage,omitempty"`

	// Online CPUs. Linux only.
	OnlineCPUs uint32 `json:"online_cpus,omitempty"`

	// Throttling Data. Linux only.
	ThrottlingData ThrottlingData `json:"throttling_data,omitempty"`
}

// MemoryStats aggregates all memory stats since container inception on Linux.
// Windows returns stats for commit and private working set only.
type MemoryStats struct {
	// Linux Memory Stats

	// current res_counter usage for memory
	Usage uint64 `json:"usage,omitempty"`
	// maximum usage ever recorded.
	MaxUsage uint64 `json:"max_usage,omitempty"`
	// TODO(vishh): Export these as stronger types.
	// all the stats exported via memory.stat.
	Stats map[string]uint64 `json:"stats,omitempty"`
	// number of times memory usage hits limits.
	Failcnt uint64 `json:"failcnt,omitempty"`
	Limit   uint64 `json:"limit,omitempty"`

	// Windows Memory Stats
	// See https://technet.microsoft.com/en-us/magazine/ff382715.aspx

	// committed bytes
	Commit uint64 `json:"commitbytes,omitempty"`
	// peak committed bytes
	CommitPeak uint64 `json:"commitpeakbytes,omitempty"`
	// private working set
	PrivateWorkingSet uint64 `json:"privateworkingset,omitempty"`
}

// BlkioStatEntry is one small entity to store a piece of Blkio stats
// Not used on Windows.
type BlkioStatEntry struct {
	Major uint64 `json:"major"`
	Minor uint64 `json:"minor"`
	Op    string `json:"op"`
	Value uint64 `json:"value"`
}

// BlkioStats stores All IO service stats for data read and write.
// This is a Linux specific structure as the differences between expressing
// block I/O on Windows and Linux are sufficiently significant to make
// little sense attempting to morph into a combined structure.
type BlkioStats struct {
	// number of bytes transferred to and from the block device
	IoServiceBytesRecursive []BlkioStatEntry `json:"io_service_bytes_recursive"`
	IoServicedRecursive     []BlkioStatEntry `json:"io_serviced_recursive"`
	IoQueuedRecursive       []BlkioStatEntry `json:"io_queue_recursive"`
	IoServiceTimeRecursive  []BlkioStatEntry `json:"io_service_time_recursive"`
	IoWaitTimeRecursive     []BlkioStatEntry `json:"io_wait_time_recursive"`
	IoMergedRecursive       []BlkioStatEntry `json:"io_merged_recursive"`
	IoTimeRecursive         []BlkioStatEntry `json:"io_time_recursive"`
	SectorsRecursive        []BlkioStatEntry `json:"sectors_recursive"`
}

// StorageStats is the disk I/O stats for read/write on Windows.
type StorageStats struct {
	ReadCountNormalized  uint64 `json:"read_count_normalized,omitempty"`
	ReadSizeBytes        uint64 `json:"read_size_bytes,omitempty"`
	WriteCountNormalized uint64 `json:"write_count_normalized,omitempty"`
	WriteSizeBytes       uint64 `json:"write_size_bytes,omitempty"`
}

// NetworkStats aggregates the network stats of one container
type NetworkStats struct {
	// Bytes received. Windows and Linux.
	RxBytes uint64 `json:"rx_bytes"`
	// Packets received. Windows and Linux.
	RxPackets uint64 `json:"rx_packets"`
	// Received errors. Not used on Windows. Note that we don't `omitempty` this
	// field as it is expected in the >=v1.21 API stats structure.
	RxErrors uint64 `json:"rx_errors"`
	// Incoming packets dropped. Windows and Linux.
	RxDropped uint64 `json:"rx_dropped"`
	// Bytes sent. Windows and Linux.
	TxBytes uint64 `json:"tx_bytes"`
	// Packets sent. Windows and Linux.
	TxPackets uint64 `json:"tx_packets"`
	// Sent errors. Not used on Windows. Note that we don't `omitempty` this
	// field as it is expected in the >=v1.21 API stats structure.
	TxErrors uint64 `json:"tx_errors"`
	// Outgoing packets dropped. Windows and Linux.
	TxDropped uint64 `json:"tx_dropped"`
	// Endpoint ID. Not used on Linux.
	EndpointID string `json:"endpoint_id,omitempty"`
	// Instance ID. Not used on Linux.
	InstanceID string `json:"instance_id,omitempty"`
}

// PidsStats contains the stats of a container's pids
type PidsStats struct {
	// Current is the number of pids in the cgroup
	Current uint64 `json:"current,omitempty"`
	// Limit is the hard limit on the number of pids in the cgroup.
	// A "Limit" of 0 means that there is no limit.
	Limit uint64 `json:"limit,omitempty"`
}

// Stats is Ultimate struct aggregating all types of stats of one container
type Stats struct {
	// Common stats
	Read    time.Time `json:"read"`
	PreRead time.Time `json:"preread"`

	// Linux specific stats, not populated on Windows.
	PidsStats  PidsStats  `json:"pids_stats,omitempty"`
	BlkioStats BlkioStats `json:"blkio_stats,omitempty"`

	// Windows specific stats, not populated on Linux.
	NumProcs     uint32       `json:"num_procs"`
	StorageStats StorageStats `json:"storage_stats,omitempty"`

	// Shared stats
	CPUStats    CPUStats    `json:"cpu_stats,omitempty"`
	PreCPUStats CPUStats    `json:"precpu_stats,omitempty"` // "Pre"="Previous"
	MemoryStats MemoryStats `json:"memory_stats,omitempty"`
}

// StatsJSON is newly used Networks
type StatsJSON struct {
	Stats

	Name string `json:"name,omitempty"`
	ID   string `json:"id,omitempty"`

	// Networks request version >=1.21
	Networks map[string]NetworkStats `json:"networks,omitempty"`
}
