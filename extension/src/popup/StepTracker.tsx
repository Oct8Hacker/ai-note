import { STEPS } from "../shared/messages";
import type { Status } from "../shared/types";
export function StepTracker({ status }: { status: Status }) {
  const index = STEPS.findIndex(
    (s) =>
      s.status ===
      (status === "COMPLETED_WITH_WARNINGS" ? "COMPLETED" : status),
  );
  return (
    <ol class="steps" aria-label="Job steps">
      {STEPS.map((s, i) => (
        <li
          key={s.status}
          aria-current={i === index ? "step" : undefined}
          class={i < index ? "past" : ""}
        >
          <span aria-hidden="true" class="step-dot">
            {i < index ? "✓" : i === index ? "●" : "·"}
          </span>
          <span>{s.label}</span>
          <span class="sr">
            {i < index ? " complete" : i === index ? " current" : " upcoming"}
          </span>
        </li>
      ))}
    </ol>
  );
}
