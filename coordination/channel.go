// coordination/channel.go
package coordination

import (
    "crypto/rand"
    "encoding/json"
    "sync"
    "time"
)

type SecureChannel struct {
    Worker1   WorkerID    `json:"worker1"`
    Worker2   WorkerID    `json:"worker2"`
    Key       []byte      `json:"key"`
    Encrypted bool        `json:"encrypted"`
    messages  chan []byte // Unexported since it can't be marshaled
    done      chan struct{}
    mu        sync.RWMutex
}

func NewSecureChannel(w1, w2 WorkerID) *SecureChannel {
    return &SecureChannel{
        Worker1:   w1,
        Worker2:   w2,
        messages:  make(chan []byte, 100),
        done:      make(chan struct{}),
    }
}

func (c *SecureChannel) EstablishSecure() error {
    // Generate session key
    key := make([]byte, 32)
    if _, err := rand.Read(key); err != nil {
        return err
    }
    
    c.mu.Lock()
    c.Key = key        // Now using exported Key
    c.Encrypted = true // Now using exported Encrypted
    c.mu.Unlock()
    
    return nil
}

func (c *SecureChannel) Send(data []byte) error {
    c.mu.RLock()
    encrypted := c.Encrypted // Now using exported Encrypted
    c.mu.RUnlock()

    if encrypted {
        var err error
        data, err = encrypt(data, c.Key) // Now using exported Key
        if err != nil {
            return err
        }
    }

    select {
    case c.messages <- data:
        return nil
    case <-time.After(5 * time.Second):
        return ErrTimeout
    }
}

func (c *SecureChannel) Receive() ([]byte, error) {
    select {
    case data := <-c.messages:
        c.mu.RLock()
        encrypted := c.Encrypted // Now using exported Encrypted
        c.mu.RUnlock()

        if encrypted {
            var err error
            data, err = decrypt(data, c.Key) // Now using exported Key
            if err != nil {
                return nil, err
            }
        }
        return data, nil
    case <-time.After(5 * time.Second):
        return nil, ErrTimeout
    }
}

func (c *SecureChannel) Close() error {
    close(c.done)
    return nil
}

// Add custom marshaling methods
func (c *SecureChannel) MarshalJSON() ([]byte, error) {
    type Alias struct {
        Worker1   WorkerID `json:"worker1"`
        Worker2   WorkerID `json:"worker2"`
        Key       []byte   `json:"key"`
        Encrypted bool     `json:"encrypted"`
    }
    
    return json.Marshal(&Alias{
        Worker1:   c.Worker1,
        Worker2:   c.Worker2,
        Key:       c.Key,
        Encrypted: c.Encrypted,
    })
}

func (c *SecureChannel) UnmarshalJSON(data []byte) error {
    type Alias struct {
        Worker1   WorkerID `json:"worker1"`
        Worker2   WorkerID `json:"worker2"`
        Key       []byte   `json:"key"`
        Encrypted bool     `json:"encrypted"`
    }
    
    aux := &Alias{}
    if err := json.Unmarshal(data, aux); err != nil {
        return err
    }
    
    c.Worker1 = aux.Worker1
    c.Worker2 = aux.Worker2
    c.Key = aux.Key
    c.Encrypted = aux.Encrypted
    
    // Reinitialize channels
    c.messages = make(chan []byte, 100)
    c.done = make(chan struct{})
    
    return nil
}

func encrypt(data, key []byte) ([]byte, error) {
    // Implement encryption
    return data, nil
}

func decrypt(data, key []byte) ([]byte, error) {
    // Implement decryption
    return data, nil
}