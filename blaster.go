package blast

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"time"

	"github.com/leemcloughlin/gofarmhash"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
)

const DEBUG = false
const INSTANT_COUNT = 100

type Blaster struct {
	config          *configDef
	viper           *viper.Viper
	rate            float64
	skip            map[farmhash.Uint128]struct{}
	dataCloser      io.Closer
	dataReader      DataReader
	dataHeaders     []string
	logCloser       io.Closer
	logWriter       LogWriteFlusher
	cancel          context.CancelFunc
	out             io.Writer
	rateInputReader io.Reader

	mainChannel            chan struct{}
	errorChannel           chan error
	workerChannel          chan workDef
	logChannel             chan logRecord
	dataFinishedChannel    chan struct{}
	workersFinishedChannel chan struct{}
	changeRateChannel      chan float64
	signalChannel          chan os.Signal

	mainWait   *sync.WaitGroup
	workerWait *sync.WaitGroup

	workerTypes map[string]func() Worker

	stats statsDef
}

type DataReader interface {
	Read() (record []string, err error)
}

type LogWriteFlusher interface {
	Write(record []string) error
	Flush()
}

type statsDef struct {
	requestsStarted         uint64
	requestsFinished        uint64
	requestsSkipped         uint64
	requestsSuccess         uint64
	requestsFailed          uint64
	requestsSuccessDuration uint64
	requestsDurationQueue   *FiloQueue
	requestsStatusQueue     *FiloQueue
	requestsStatusTotal     *ThreadSaveMapIntInt

	workersBusy  int64
	ticksSkipped uint64
}

func New(ctx context.Context, cancel context.CancelFunc) *Blaster {

	b := &Blaster{
		viper:                  viper.New(),
		cancel:                 cancel,
		mainWait:               new(sync.WaitGroup),
		workerWait:             new(sync.WaitGroup),
		workerTypes:            make(map[string]func() Worker),
		dataFinishedChannel:    make(chan struct{}),
		workersFinishedChannel: make(chan struct{}),
		changeRateChannel:      make(chan float64, 1),
		stats: statsDef{
			requestsDurationQueue: &FiloQueue{},
			requestsStatusQueue:   &FiloQueue{},
			requestsStatusTotal:   NewThreadSaveMapIntInt(),
		},
	}

	// trap Ctrl+C and call cancel on the context
	b.signalChannel = make(chan os.Signal, 1)
	signal.Notify(b.signalChannel, os.Interrupt)
	go func() {
		select {
		case <-b.signalChannel:
			b.cancel()
		case <-ctx.Done():
		}
	}()

	return b
}

func (b *Blaster) Exit() {
	signal.Stop(b.signalChannel)
	b.cancel()
}

func (b *Blaster) Start(ctx context.Context) error {

	b.out = os.Stdout

	if err := b.loadConfigViper(); err != nil {
		return err
	}

	if b.config.Data == "" {
		return errors.New("No data file specified. Use --config to view current config.")
	}

	if err := b.openDataFile(ctx); err != nil {
		return err
	}
	defer b.closeDataFile()

	if err := b.openLogAndInit(); err != nil {
		return err
	}
	defer b.flushAndCloseLog()

	b.rateInputReader = os.Stdin

	return b.start(ctx)
}

func (b *Blaster) start(ctx context.Context) error {

	b.startTickerLoop(ctx)
	b.startMainLoop(ctx)
	b.startErrorLoop(ctx)
	b.startWorkers(ctx)
	b.startLogLoop(ctx)
	b.startStatusLoop(ctx)
	b.startRateLoop(ctx)

	b.printRatePrompt()

	// wait for cancel or finished
	select {
	case <-ctx.Done():
	case <-b.dataFinishedChannel:
	}

	fmt.Fprintln(b.out, "Waiting for workers to finish...")
	b.workerWait.Wait()

	// signal to log and error loop that it's tine to exit
	close(b.workersFinishedChannel)

	fmt.Fprintln(b.out, "Waiting for processes to finish...")
	b.mainWait.Wait()

	b.printStatus(true)

	return nil
}

func (b *Blaster) RegisterWorkerType(key string, workerFunc func() Worker) {
	b.workerTypes[key] = workerFunc
}

type Worker interface {
	Send(ctx context.Context, payload map[string]interface{}) (response map[string]interface{}, err error)
}

type Starter interface {
	Start(ctx context.Context, payload map[string]interface{}) error
}

type Stopper interface {
	Stop(ctx context.Context, payload map[string]interface{}) error
}

func init() {
	if DEBUG {
		go func() {
			// debug to see if goroutines aren't being closed...
			ticker := time.NewTicker(time.Millisecond * 200)
			for range ticker.C {
				fmt.Println("runtime.NumGoroutine(): ", runtime.NumGoroutine())
			}
		}()
	}
}
