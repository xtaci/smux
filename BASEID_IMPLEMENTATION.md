# Реализация функции BaseID для smux

## Описание

Добавлена функциональность для создания стримов с базовым ID, который может быть передан между сторонами канала. Это позволяет связать стримы с внешними идентификаторами или метаданными.

## Ключевые изменения

### 1. **stream.go** - Добавление поля baseID

Добавлено новое поле в структуру `stream`:

```go
type stream struct {
    id     uint32 // Stream identifier
    baseID uint32 // Base ID used to generate the stream ID  // NEW
    sess   *Session
    // ...
}
```

### 2. **stream.go** - Новые функции инициализации

Создана новая функция `newStreamWithBaseID()` для инициализации стрима с базовым ID:

```go
// newStreamWithBaseID initializes and returns a new Stream with a base ID.
func newStreamWithBaseID(id uint32, baseID uint32, frameSize int, sess *Session) *stream
```

Функция `newStream()` теперь вызывает `newStreamWithBaseID()` с `baseID = 0` для обратной совместимости.

### 3. **stream.go** - Публичный метод BaseID()

Добавлен новый публичный метод для получения базового ID:

```go
// BaseID returns the base ID used to generate the stream identifier.
func (s *stream) BaseID() uint32 {
    return s.baseID
}
```

### 4. **session.go** - Новый метод OpenStreamWithBaseID()

Добавлены три новых метода:

```go
// OpenStream - теперь делегирует openStreamInternal(0)
func (s *Session) OpenStream() (*Stream, error) {
    return s.openStreamInternal(0)
}

// OpenStreamWithBaseID - новый метод для создания стрима с базовым ID
func (s *Session) OpenStreamWithBaseID(baseID uint32) (*Stream, error) {
    return s.openStreamInternal(baseID)
}

// openStreamInternal - внутренний метод для создания стримов
func (s *Session) openStreamInternal(baseID uint32) (*Stream, error)
```

#### Передача baseID через фрейм SYN

Когда `baseID != 0`, он передается в payload SYN фрейма (4 байта, little-endian):

```go
// Create SYN frame
synFrame := newFrame(byte(s.config.Version), cmdSYN, sid)
if baseID != 0 {
    // Include baseID in the payload when it's non-zero
    synFrame.data = make([]byte, 4)
    binary.LittleEndian.PutUint32(synFrame.data, baseID)
}
```

### 5. **session.go** - Обработка входящего SYN фрейма

Обновлена обработка `cmdSYN` для чтения baseID из payload:

```go
case cmdSYN: // stream opening
    var accepted *stream
    s.streamLock.Lock()
    if _, ok := s.streams[sid]; !ok {
        var baseID uint32 = 0
        
        // If there's payload, read baseID (4 bytes)
        if hdr.Length() > 0 {
            synPayloadBuf := make([]byte, hdr.Length())
            _, err := io.ReadFull(s.conn, synPayloadBuf)
            if err != nil {
                s.streamLock.Unlock()
                s.notifyReadError(err)
                return
            }
            
            // Extract baseID from the first 4 bytes if available
            if len(synPayloadBuf) >= 4 {
                baseID = binary.LittleEndian.Uint32(synPayloadBuf[:4])
            }
        }
        
        if baseID == 0 {
            stream := newStream(sid, s.config.MaxFrameSize, s)
            s.streams[sid] = stream
            accepted = stream
        } else {
            stream := newStreamWithBaseID(sid, baseID, s.config.MaxFrameSize, s)
            s.streams[sid] = stream
            accepted = stream
        }
    }
    // ...
```

## Обратная совместимость

✅ **Сохранена полная обратная совместимость:**

1. Метод `OpenStream()` продолжает работать без изменений
2. Старые стримы без baseID работают как прежде (baseID = 0)
3. Во время передачи, если baseID = 0, payload не отправляется в SYN фрейм
4. На принимающей стороне, если payload отсутствует, baseID инициализируется как 0

## Использование

### Создание стрима с базовым ID (клиент)

```go
clientSession, _ := smux.Client(conn, nil)

// Создаем стрим с baseID = 12345
stream, _ := clientSession.OpenStreamWithBaseID(12345)
defer stream.Close()

// Используем как обычный стрим
stream.Write([]byte("data"))
```

### Получение базового ID (сервер)

```go
serverSession, _ := smux.Server(conn, nil)

// Принимаем стрим
stream, _ := serverSession.AcceptStream()
defer stream.Close()

// Получаем базовый ID
baseID := stream.BaseID()
fmt.Printf("Received stream with baseID: %d\n", baseID)

// Используем как обычный стрим
stream.Read(buf)
```

## Тестирование

Созданы следующие тесты в `baseid_test.go`:

1. **TestOpenStreamWithBaseID** - Проверяет передачу и получение baseID
2. **TestOpenStreamWithBaseIDZero** - Проверяет, что baseID=0 работает как OpenStream()
3. **TestOpenStreamBackwardCompatibility** - Проверяет, что старый код OpenStream() работает

## Преимущества

- 🔄 **Связь идентификаторов**: Можно связать внешний ID с внутренним ID стрима
- 📊 **Метаданные**: Передача метаинформации между сторонами канала
- ⚙️ **Простота**: Минимальные изменения в протоколе
- ✅ **Совместимость**: Полная обратная совместимость с существующим кодом
- 📦 **Эффективность**: BaseID передается в payload существующего SYN фрейма
