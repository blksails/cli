// Minimal GoTrue /token contract used by the real SDK's refresh path.
// Public owner mock lookup was unavailable because Postman CLI is signed out.
const http = require('http');
http.createServer(async (req, res) => {
  const url = new URL(req.url, 'http://localhost');
  if (req.method !== 'POST' || url.pathname !== '/token') {
    res.writeHead(501); return res.end('Endpoint not modeled');
  }
  const body = req.body || {};
  if (url.searchParams.get('grant_type') !== 'refresh_token' || !body.refresh_token) {
    res.writeHead(400, {'Content-Type': 'application/json'});
    return res.end(JSON.stringify({error: 'invalid_request'}));
  }
  if (body.refresh_token === 'outage') {
    res.writeHead(502, {'Content-Type': 'text/html'});
    return res.end('<html>Bad Gateway</html>');
  }
  res.setHeader('Content-Type', 'application/json');
  if (body.refresh_token === 'revoked') {
    res.writeHead(400);
    return res.end(JSON.stringify({error_code: 'refresh_token_not_found', msg: 'Invalid Refresh Token: Refresh Token Not Found'}));
  }
  res.end(JSON.stringify({access_token:'test-access', refresh_token:'test-rotated', token_type:'bearer', expires_in:3600,
    expires_at:Math.floor(Date.now()/1000)+3600, user:{id:'00000000-0000-0000-0000-000000000001', email:'test@example.com'}}));
}).listen(process.env.PORT);
