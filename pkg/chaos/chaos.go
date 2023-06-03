package chaos

import (
	"fmt"
	"github.com/pkg/errors"
	"github.com/shchiv/notifier-service/pkg/log"
	"golang.org/x/exp/rand"
	"os"
	"strconv"
	"strings"
	"time"
)

type Mode string

const (
	Default   Mode = ""          // Soft
	Soft      Mode = "soft"      // probability: 1%
	Hard           = "hard"      // probability: 2%
	Critical       = "critical"  // probability: 10%
	Nightmare      = "nightmare" // probability: 33%
	Hell           = "hell"      // probability: 100%
)

var (
	startTime = time.Now()

	NightmareException = errors.New("nightmare")
	HellException      = errors.New("hell")
)

func parseChaosMode(str string) Mode {
	switch str {
	case "":
		return Default
	case "soft":
		return Soft
	case "hard":
		return Hard
	case "critical":
		return Critical
	case "nightmare":
		return Nightmare
	case "hell":
		return Hell
	default:
		panic(fmt.Sprintf("Unknown Chaos Mode: '%s'", str))
	}
}

type Assault string

const (
	LatencyAssaultEnvVar   Assault = "CHAOS_MONKEY_LATENCY"
	ExceptionAssaultEnvVar Assault = "CHAOS_MONKEY_EXCEPTION"
	//AppKillerAssault ChaosAssault = "CHAOS_MONKEY_APP_KILLER"
	//MemoryAssault    ChaosAssault = "CHAOS_MONKEY_MEMORY"
	//CPUAssault       ChaosAssault = "CHAOS_MONKEY_CPU"
)

type Config struct {
	Enabled         bool  // CHAOS_CLIENT_ENABLED
	primaryMode     Mode  // CHAOS_CLIENT_PRIMARY_MODE
	secondaryMode   Mode  // CHAOS_CLIENT_SECONDARY_MODE
	minLatency      int64 // CHAOS_MONKEY_LATENCY_MIN_TIME
	maxLatency      int64 // CHAOS_MONKEY_LATENCY_MAX_TIME
	delayMinutes    int64 // CHAOS_MONKEY_DELAY_START
	durationMinutes int64 // CHAOS_MONKEY_DURATION

	PrimaryOccurrenceRange   []int
	SecondaryOccurrenceRange []int
	Assaults                 []Assault // map[ChaosAssault]bool
}

var cfg Config

func ParseConfig() Config {
	minTime := os.Getenv("CHAOS_MONKEY_LATENCY_MIN_TIME")
	maxTime := os.Getenv("CHAOS_MONKEY_LATENCY_MAX_TIME")

	if maxTime == "" {
		maxTime = "1000"
	}

	maxTimeInt, err := strconv.ParseInt(maxTime, 10, 64)
	if err != nil {
		panic(err)
	}
	if minTime == "" {
		minTime = maxTime
	}
	minTimeInt, err := strconv.ParseInt(minTime, 10, 64)
	if err != nil {
		panic(err)
	}

	var delayMinutes int64
	if delay, ok := os.LookupEnv("CHAOS_MONKEY_DELAY_START"); ok {
		d, err := strconv.ParseInt(delay, 10, 64)
		if err == nil {
			delayMinutes = d
		} else {
			log.Logger().Infof("can't parse CHAOS_MONKEY_DELAY_START '%s': %v", delay, err)
		}
	}

	var durationMinutes int64
	if dur, ok := os.LookupEnv("CHAOS_MONKEY_DURATION"); ok {
		d, err := strconv.ParseInt(dur, 10, 64)
		if err == nil {
			durationMinutes = d
		} else {
			log.Logger().Infof("can't parse CHAOS_MONKEY_DURATION '%s': %v", dur, err)
		}
	}

	retVal := Config{
		Enabled:         os.Getenv("CHAOS_MONKEY_ENABLED") == "true",
		minLatency:      minTimeInt,
		maxLatency:      maxTimeInt,
		primaryMode:     parseChaosMode(os.Getenv("CHAOS_MONKEY_PRIMARY_MODE")),
		secondaryMode:   parseChaosMode(os.Getenv("CHAOS_MONKEY_SECONDARY_MODE")),
		delayMinutes:    delayMinutes,
		durationMinutes: durationMinutes,
	}

	for _, a := range []Assault{ExceptionAssaultEnvVar, LatencyAssaultEnvVar} {
		assault := fmt.Sprintf("%v", a)

		if strings.ToLower(os.Getenv(strings.ToUpper(assault))) == "true" {
			retVal.Assaults = append(retVal.Assaults, a)
		}
	}

	// -- Calculate occurrence range
	rand.Seed(uint64(rand.Int63n(10000)))
	modes := map[Mode]int{
		Default:   100,
		Soft:      100,
		Hard:      50,
		Critical:  10,
		Nightmare: 3,
		Hell:      0,
	}

	retVal.PrimaryOccurrenceRange = make([]int, modes[retVal.primaryMode])
	for i := range retVal.PrimaryOccurrenceRange {
		retVal.PrimaryOccurrenceRange[i] = i
	}

	if retVal.secondaryMode != "" {
		retVal.SecondaryOccurrenceRange = make([]int, modes[retVal.secondaryMode])
		for i := range retVal.SecondaryOccurrenceRange {
			retVal.SecondaryOccurrenceRange[i] = i
		}
	}

	return retVal
}

func ExceptionAssault(err error) error {
	//fmt.Println("[CHAOS MONKEY] - Exception Assault")
	return fmt.Errorf("chaos fake produced for mode: %w", err)
}

func LatencyAssault() {
	//fmt.Println("[CHAOS MONKEY] - Latency Assault")
	latency := RandInt64(cfg.minLatency, cfg.maxLatency)
	//fmt.Printf("[CHAOS MONKEY] Latency Injected: %v ms\n", latencyToInject)
	time.Sleep(time.Duration(int64(time.Millisecond) * latency))
}

func RandInt64(min, max int64) int64 {
	if min >= max || min == 0 || max == 0 {
		return max
	}
	return rand.Int63n(max-min) + min
}

func Start(cfg Config) error {
	if cfg.Enabled {
		if cfg.primaryMode == "" {
			cfg.primaryMode = Soft
		}

		if cfg.secondaryMode != "" {
			if cfg.delayMinutes > 0 {
				modeStartTime := startTime.Add(time.Duration(cfg.delayMinutes) * time.Minute)
				if time.Now().Before(modeStartTime) {
					goto primary
				}
			}

			if cfg.durationMinutes > 0 {
				var modeStartTime, modeStopTime time.Time
				if cfg.delayMinutes != 0 {
					modeStartTime = startTime.Add(time.Duration(cfg.delayMinutes) * time.Minute)
				} else {
					modeStartTime = startTime
				}
				modeStopTime = modeStartTime.Add(time.Duration(cfg.durationMinutes) * time.Minute)
				if time.Now().After(modeStopTime) {
					startTime = time.Now()
					goto primary
				}
			}

			if len(cfg.SecondaryOccurrenceRange) == 0 ||
				cfg.SecondaryOccurrenceRange[rand.Intn(len(cfg.SecondaryOccurrenceRange))] == 0 {
				rand.Seed(uint64(rand.Int63n(10000)))
				assault := cfg.Assaults[rand.Intn(len(cfg.Assaults))]

				switch assault {
				case "CHAOS_MONKEY_LATENCY":
					// skipped for Hell mode
					fallthrough
				case "CHAOS_MONKEY_EXCEPTION":
					// hardcoded value for test
					return ExceptionAssault(HellException)
				default:
					panic(fmt.Sprintf("Unknown chaos assault '%s'", assault))
				}
			}
		}

	primary:
		if len(cfg.PrimaryOccurrenceRange) == 0 ||
			cfg.PrimaryOccurrenceRange[rand.Intn(len(cfg.PrimaryOccurrenceRange))] == 0 {
			rand.Seed(uint64(rand.Int63n(10000)))
			assault := cfg.Assaults[rand.Intn(len(cfg.Assaults))]

			switch assault {
			case "CHAOS_MONKEY_LATENCY":
				LatencyAssault()
				return nil
			case "CHAOS_MONKEY_EXCEPTION":
				// hardcoded value for test
				return ExceptionAssault(NightmareException)
			default:
				panic(fmt.Sprintf("Unknown chaos assault '%s'", assault))
			}
		}
	}
	return nil
}
