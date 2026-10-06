// SPDX-License-Identifier: GPL-3.0-only
//
// Adapted from go-librespot's session/client_token.go
// (https://github.com/devgianlu/go-librespot, GPL-3.0). That package can't
// be imported here because it needs cgo for its audio decoders. GPL-3.0
// code may be combined with Syncphony's AGPL-3.0 code (GPL-3.0 section 13);
// this file stays under GPL-3.0.

package streaming

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	golibrespot "github.com/devgianlu/go-librespot"
	pbdata "github.com/devgianlu/go-librespot/proto/spotify/clienttoken/data/v0"
	pbhttp "github.com/devgianlu/go-librespot/proto/spotify/clienttoken/http/v0"
	"google.golang.org/protobuf/proto"
)

const clientTokenURL = "https://clienttoken.spotify.com/v1/clienttoken" //nolint:gosec // a URL, not a credential

// retrieveClientToken gets the client token login5 and spclient need.
func retrieveClientToken(ctx context.Context, c *http.Client, deviceID string) (string, error) {
	body, err := proto.Marshal(&pbhttp.ClientTokenRequest{
		RequestType: pbhttp.ClientTokenRequestType_REQUEST_CLIENT_DATA_REQUEST,
		Request: &pbhttp.ClientTokenRequest_ClientData{
			ClientData: &pbhttp.ClientDataRequest{
				ClientId:      golibrespot.ClientIdHex,
				ClientVersion: golibrespot.SpotifyLikeClientVersion(),
				Data: &pbhttp.ClientDataRequest_ConnectivitySdkData{
					ConnectivitySdkData: &pbdata.ConnectivitySdkData{
						DeviceId:             deviceID,
						PlatformSpecificData: golibrespot.GetPlatformSpecificData(),
					},
				},
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("marshalling ClientTokenRequest: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, clientTokenURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/x-protobuf")
	req.Header.Set("User-Agent", golibrespot.UserAgent())
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("clienttoken: HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	var out pbhttp.ClientTokenResponse
	if err := proto.Unmarshal(b, &out); err != nil {
		return "", fmt.Errorf("unmarshalling ClientTokenResponse: %w", err)
	}
	if out.GetResponseType() != pbhttp.ClientTokenResponseType_RESPONSE_GRANTED_TOKEN_RESPONSE {
		return "", fmt.Errorf("clienttoken: response type %v", out.GetResponseType())
	}
	return out.GetGrantedToken().GetToken(), nil
}
