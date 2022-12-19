package utils

import (
	"context"
	"log"
	"sync"
)

type Closer struct {
	currentLock sync.Mutex
	closed      bool
	ctx         context.Context
	cancel      context.CancelFunc
	onClose     func()
}

func (c *Closer) Ctx() context.Context {
	return c.ctx
}

func (c *Closer) SetOnClose(onClose func()) {
	c.onClose = onClose
}

func (c *Closer) IsClosed() bool {
	defer c.currentLock.Unlock()
	c.currentLock.Lock()
	return c.closed
}

func (c *Closer) CtxCancel() {
	if c.cancel != nil {
		c.cancel()
	}
}

func (c *Closer) SetCtx(parent context.Context) {
	if parent != nil {
		if c.ctx != nil && !c.closed {
			log.Println("[Warning] context is not nil and context is not closed")
		}
		c.ctx, c.cancel = context.WithCancel(parent)
		c.closed = false
		go func() {
			select {
			case <-c.ctx.Done():
				defer c.currentLock.Unlock()
				c.currentLock.Lock()

				if !c.closed {
					if c.onClose != nil {
						c.onClose()
					}
					c.closed = true
				}
			}
		}()
	}
}
