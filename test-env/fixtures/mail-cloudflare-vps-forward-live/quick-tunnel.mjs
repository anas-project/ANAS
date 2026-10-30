// TEST_CASES: MAILMOD-T-009 MAILMOD-T-080
// Only the framed provisioning result is a tunnel endpoint. An API URL in an
// error message must not be mistaken for one.
export function quickTunnelOrigin(line){
 const match=line.match(/\|\s+(https:\/\/([a-z0-9](?:[a-z0-9-]*[a-z0-9])?)\.trycloudflare\.com)\s+\|/);
 return match&&match[2]!=='api'?match[1]:null;
}
