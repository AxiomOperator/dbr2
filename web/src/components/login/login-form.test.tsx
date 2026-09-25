// SPDX-License-Identifier: Apache-2.0
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithQuery, routeFetch } from "@/test/render";
import { LoginForm } from "./login-form";

const ME = {
  id: "1",
  username: "admin",
  display_name: "Master Admin",
  email: null,
  kind: "master_admin",
  roles: ["administrator"],
  permissions: ["audit.read"],
  totp_enabled: true,
};

function problem(status: number, code: string, headers: Record<string, string> = {}) {
  return new Response(JSON.stringify({ title: "Error", status, detail: code, code }), {
    status,
    headers: { "content-type": "application/problem+json", ...headers },
  });
}

describe("LoginForm", () => {
  it("asks for a TOTP code on totp_required and resubmits with the same credentials", async () => {
    const bodies: unknown[] = [];
    const fetchMock = vi.fn(
      routeFetch({
        "POST /api/v1/auth/login": (init) => {
          const body = JSON.parse(String(init?.body));
          bodies.push(body);
          if (!body.totp_code) return problem(401, "totp_required");
          if (body.totp_code !== "123456") return problem(401, "invalid_totp");
          return Response.json({ user: ME });
        },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const onSuccess = vi.fn();
    const user = userEvent.setup();

    renderWithQuery(<LoginForm onSuccess={onSuccess} />);

    await user.type(screen.getByLabelText("Username"), "admin");
    await user.type(screen.getByLabelText("Password"), "correct-horse-battery");
    await user.click(screen.getByRole("button", { name: "Sign in" }));

    // TOTP step replaces the password fields and takes focus.
    const totp = await screen.findByLabelText("Authentication code");
    await waitFor(() => expect(totp).toHaveFocus());
    expect(screen.queryByLabelText("Password")).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();

    // A wrong code shows an error and stays on the TOTP step.
    await user.type(totp, "000000");
    await user.click(screen.getByRole("button", { name: "Verify" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("authentication code is not valid");
    expect(screen.getByLabelText("Authentication code")).toBeInTheDocument();

    await user.type(screen.getByLabelText("Authentication code"), "123456");
    await user.click(screen.getByRole("button", { name: "Verify" }));

    await waitFor(() => expect(onSuccess).toHaveBeenCalledWith(ME));
    expect(bodies).toEqual([
      { username: "admin", password: "correct-horse-battery" },
      { username: "admin", password: "correct-horse-battery", totp_code: "000000" },
      { username: "admin", password: "correct-horse-battery", totp_code: "123456" },
    ]);
  });

  it("validates the TOTP code client-side before submitting", async () => {
    const fetchMock = vi.fn(
      routeFetch({ "POST /api/v1/auth/login": () => problem(401, "totp_required") }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    renderWithQuery(<LoginForm onSuccess={vi.fn()} />);

    await user.type(screen.getByLabelText("Username"), "admin");
    await user.type(screen.getByLabelText("Password"), "pw");
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    await user.type(await screen.findByLabelText("Authentication code"), "12");
    await user.click(screen.getByRole("button", { name: "Verify" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("6-digit code");
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("shows a lockout message using Retry-After", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        routeFetch({
          "POST /api/v1/auth/login": () => problem(423, "account_locked", { "retry-after": "90" }),
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithQuery(<LoginForm onSuccess={vi.fn()} />);

    await user.type(screen.getByLabelText("Username"), "admin");
    await user.type(screen.getByLabelText("Password"), "wrong");
    await user.click(screen.getByRole("button", { name: "Sign in" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "temporarily locked after too many failed sign-in attempts. Try again in 2 minutes.",
    );
  });

  it("clears the password on invalid_credentials", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(routeFetch({ "POST /api/v1/auth/login": () => problem(401, "invalid_credentials") })),
    );
    const user = userEvent.setup();
    renderWithQuery(<LoginForm onSuccess={vi.fn()} />);

    await user.type(screen.getByLabelText("Username"), "admin");
    await user.type(screen.getByLabelText("Password"), "wrong");
    await user.click(screen.getByRole("button", { name: "Sign in" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Incorrect username or password.");
    expect(screen.getByLabelText("Password")).toHaveValue("");
    expect(screen.getByLabelText("Username")).toHaveValue("admin");
  });
});
