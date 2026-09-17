import http from "k6/http";
export const options = { vus: 50, duration: "60s", maxRedirects: 0 };
export default function () {
  http.get("http://localhost:8080/r/oRo9eE");
}
