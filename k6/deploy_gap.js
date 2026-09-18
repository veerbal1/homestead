import http from "k6/http";
export const options = {
  scenarios: {
    steady: {
      executor: "constant-arrival-rate",
      rate: 100,
      timeUnit: "1s",
      duration: "90s",
      preAllocatedVUs: 100,
      maxVUs: 500,
    },
  },
};
export default function () {
  http.get("http://localhost:8080/r/oRo9eE", { redirects: 0 });
}
