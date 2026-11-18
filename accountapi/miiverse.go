package accountapi

import (
	"encoding/xml"
	"net/http"
)

// MiiversePath is where a console is told Miiverse is. Its discovery
// request goes to MiiversePath + "/v1/endpoint"
const MiiversePath = "/olv"

// * Where bundles made before Miiverse was answered point Cemu, so those
// * players need no new settings
const oldMiiversePath = "/unserved/olv"

// * Super Mario Maker starts Miiverse before uploading a course, to post
// * about it afterwards. If Miiverse does not start, the game shows
// * error 115-1101 although the course was uploaded. There is no
// * Miiverse here, so the stand-in only says where it is, and answers
// * anything else with an empty success. Cemu makes no other request:
// * it answers post downloads with no posts and fakes posting itself
func registerMiiverse(mux *http.ServeMux) {
	mux.HandleFunc("GET "+MiiversePath+"/v1/endpoint", miiverseDiscovery)
	mux.HandleFunc("GET "+oldMiiversePath, miiverseDiscovery)
	mux.HandleFunc(MiiversePath+"/", miiverseNothing)
}

type miiverseResult struct {
	XMLName  xml.Name          `xml:"result"`
	HasError int               `xml:"has_error"`
	Version  int               `xml:"version"`
	Endpoint *miiverseEndpoint `xml:"endpoint,omitempty"`
}

// * Hosts are written without https://, which the console adds
type miiverseEndpoint struct {
	Host       string `xml:"host"`
	APIHost    string `xml:"api_host"`
	PortalHost string `xml:"portal_host"`
	N3DSHost   string `xml:"n3ds_host"`
}

func miiverseDiscovery(w http.ResponseWriter, r *http.Request) {
	host := r.Host + MiiversePath

	writeXML(w, http.StatusOK, miiverseResult{
		Version:  1,
		Endpoint: &miiverseEndpoint{Host: host, APIHost: host, PortalHost: host, N3DSHost: host},
	})
}

func miiverseNothing(w http.ResponseWriter, r *http.Request) {
	writeXML(w, http.StatusOK, miiverseResult{Version: 1})
}
