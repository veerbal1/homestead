import http from "k6/http";
export const options = {
  scenarios: {
    ramp: {
      executor: "ramping-arrival-rate",
      startRate: 100,
      timeUnit: "1s",
      preAllocatedVUs: 200,
      maxVUs: 3000,
      stages: [
        { target: 500, duration: "30s" },
        { target: 1000, duration: "30s" },
        { target: 2000, duration: "30s" },
        { target: 4000, duration: "30s" },
      ],
    },
  },
};
export default function () {
  http.get("https://homestead.undercoverdevs.com/r/ulsLfD", { redirects: 0 });
}
