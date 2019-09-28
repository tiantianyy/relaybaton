package relaybaton

import (
	"encoding/binary"
	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
	"io"
	"sync"
)

type peer struct {
	connPool     *connectionPool
	mutexWsRead  sync.Mutex
	controlQueue chan *websocket.PreparedMessage
	messageQueue chan *websocket.PreparedMessage
	hasMessage   chan byte
	quit         chan byte
	wsConn       *websocket.Conn
	conf         Config
}

func (peer *peer) init(conf Config) {
	peer.hasMessage = make(chan byte, 2^32+2^16)
	peer.controlQueue = make(chan *websocket.PreparedMessage, 2^16)
	peer.messageQueue = make(chan *websocket.PreparedMessage, 2^32)
	peer.connPool = NewConnectionPool()
	peer.quit = make(chan byte, 3)
	peer.conf = conf
}

func (peer *peer) forward(session uint16) {
	wsw := peer.getWebsocketWriter(session)
	conn := peer.connPool.get(session)
	_, err := io.Copy(wsw, *conn)
	if err != nil {
		log.Error(err)
	}
	err = (*conn).Close()
	if err != nil {
		log.WithField("session", session).Error(err)
	}
	_, err = wsw.writeClose()
	if err != nil {
		log.WithField("session", session).Error(err)
	}
	peer.connPool.delete(session)
}

func (peer *peer) receive(session uint16, data []byte) {
	wsw := peer.getWebsocketWriter(session)
	conn := peer.connPool.get(session)
	if conn == nil {
		if peer.connPool.isCloseSent(session) {
			return
		}
		log.WithField("session", session).Debug("deleted connection read")
		_, err := wsw.writeClose()
		if err != nil {
			log.Error(err)
		}
		return
	}
	_, err := (*conn).Write(data)
	if err != nil {
		log.WithField("session", session).Error(err)
		err = (*conn).Close()
		if err != nil {
			log.Error(err)
		}
		_, err = wsw.writeClose()
		if err != nil {
			log.Error(err)
		}
		peer.connPool.delete(session)
	}
}

func (peer *peer) delete(session uint16) {
	conn := peer.connPool.get(session)
	if conn != nil {
		err := (*conn).Close()
		if err != nil {
			log.Error(err)
		}
		peer.connPool.delete(session)

		log.Debugf("Port %d Deleted", session)
	}
	peer.connPool.setCloseSent(session)
}

func (peer *peer) getWebsocketWriter(session uint16) webSocketWriter {
	return webSocketWriter{
		session: session,
		peer:    peer,
	}
}

func (peer *peer) processQueue() {
	for {
		select {
		case <-peer.quit:
			return
		default:
			<-peer.hasMessage
			if (len(peer.hasMessage)+1)%50 == 0 {
				log.WithField("len", len(peer.hasMessage)+1).Debug("Message Length") //test
			}
			if len(peer.controlQueue) > 0 {
				err := peer.wsConn.WritePreparedMessage(<-peer.controlQueue)
				if err != nil {
					log.Error(err)
					peer.Close()
					return
				}
			} else {
				err := peer.wsConn.WritePreparedMessage(<-peer.messageQueue)
				if err != nil {
					log.Error(err)
					peer.Close()
					return
				}
			}
		}
	}
}

func (peer *peer) Close() {
	log.Debug("closing peer")
	peer.mutexWsRead.Unlock()
	peer.quit <- 0
	peer.quit <- 1
	peer.quit <- 2
}

func uint16ToBytes(n uint16) []byte {
	buf := make([]byte, 2)
	binary.BigEndian.PutUint16(buf, n)
	return buf
}
