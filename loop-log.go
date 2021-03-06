package blast

import (
	"context"
	"fmt"
)

func (b *Blaster) startLogLoop(ctx context.Context) {

	b.mainWait.Add(1)
	b.logChannel = make(chan logRecord)

	go func() {
		defer fmt.Fprintln(b.out, "Exiting log loop")
		defer b.mainWait.Done()
		var count uint64
		for {
			count++
			select {
			// don't react to ctx.Done() here because we may need to wait until workers have finished
			case <-b.workersFinishedChannel:
				// exit gracefully
				return
			case lr := <-b.logChannel:
				b.logWriter.Write(lr.ToCsv())
				if count%1000 == 0 {
					b.logWriter.Flush()
				}
			}
		}
	}()
}
