package runtime

// Schema 2 predates local protocol parsing. Keep its fixed-width field order
// separate from SocketState so old modeled sockets can resume without replaying
// previously written bytes as fresh requests to a newly supported server.
type socketStateV2 struct {
	ID                  ServiceID
	Owner               OwnerID
	Domain, Type        int32
	State               ConnectionState
	Host                string
	Address             uint32
	Port                uint16
	ReadData, WriteData []byte
}

type networkStateV2 struct {
	Limits  NetworkLimits
	Sockets []socketStateV2
	HTTP    []HTTPState
	Serial  []SerialState
}

func decodeLegacyNetworkState(data []byte) (NetworkState, error) {
	var old networkStateV2
	if err := decodeStateValue(data, &old); err != nil {
		return NetworkState{}, err
	}
	current := NetworkState{Limits: old.Limits, HTTP: old.HTTP, Serial: old.Serial}
	if old.Sockets != nil {
		current.Sockets = make([]SocketState, len(old.Sockets))
	}
	for i, socket := range old.Sockets {
		current.Sockets[i] = SocketState{
			ID: socket.ID, Owner: socket.Owner, Domain: socket.Domain, Type: socket.Type,
			State: socket.State, Host: socket.Host, Address: socket.Address, Port: socket.Port,
			ReadData: socket.ReadData, WriteData: socket.WriteData,
		}
		if localServerKind(socket.Host, socket.Port, socket.Type) != "" {
			current.Sockets[i].LocalCursor = uint64(len(socket.WriteData))
		}
	}
	return current, nil
}
