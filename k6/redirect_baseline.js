import http from 'k6/http';
export const options = { vus: 50, iterations: 5000, maxRedirects: 0 };
export default function () {
  http.get('http://localhost:8080/r/mZ3E5s');
}