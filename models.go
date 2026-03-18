package main

type Config struct {
	Name   string `json:"name"`
	Config string `json:"config"`
}

type HandshakeRequest struct {
	DeviceID string `json:"device_id"`
}

type hsresp struct {
	Token string `json:"token"`
}

type S_con struct {
	Sid string `json:"id"`
}

type Is struct {
	I1 string `json:"i1"`
}
type CrcsReq struct {
	Name           string `json:"name"`
	ApplyISettings bool   `json:"apply_i_settings"`
	ISettings      Is     `json:"i_settings"`
}

type CrcsResp struct {
	Client Clops `json:"client"`
}
type Clops struct {
	Clid string `json:"id"`
}

type Getcfg struct {
	Clncfg string `json:"clean_config"`
}