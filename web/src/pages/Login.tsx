import { useState } from "react";
import { useNavigate, useLocation } from "react-router";
import { useApi } from "@/api/useApi";

export function Login() {
  const [tokenInput, setTokenInput] = useState("");
  const [error, setError] = useState("");
  const [isLoading, setIsLoading] = useState(false);
  const { setToken, validateToken } = useApi();
  const navigate = useNavigate();
  const location = useLocation();

  const from = location.state?.from?.pathname || "/";

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!tokenInput.trim()) return;

    setIsLoading(true);
    setError("");

    try {
      // Validate token first without storing it (avoids onUnauthorized callback)
      const isValid = await validateToken(tokenInput);

      if (isValid) {
        // Only persist token after successful validation
        setToken(tokenInput);
        navigate(from, { replace: true });
      } else {
        setError("Invalid token");
      }
    } catch (err: unknown) {
      console.error("Login error:", err);
      setError("Connection failed, please check your network");
    } finally {
      setIsLoading(false);
    }
  };

  return (
    <div className="min-h-screen flex items-center justify-center bg-bg-secondary py-12 px-4 sm:px-6 lg:px-8">
      <div className="max-w-md w-full space-y-8">
        <div>
          <h2 className="mt-6 text-center text-3xl font-extrabold text-text-primary">
            Switch-A Admin
          </h2>
          <p className="mt-2 text-center text-sm text-text-secondary">
            Please enter your admin token to continue
          </p>
        </div>
        <form className="mt-8 space-y-6" onSubmit={handleSubmit}>
          <div className="rounded-md shadow-sm -space-y-px">
            <div>
              <label htmlFor="token" className="sr-only">
                Admin Token
              </label>
              <input
                id="token"
                name="token"
                type="password"
                autoComplete="current-password"
                required
                className="input"
                placeholder="Admin Token"
                value={tokenInput}
                onChange={(e) => setTokenInput(e.target.value)}
              />
            </div>
          </div>

          {error && (
            <div
              role="alert"
              className="text-danger text-sm text-center bg-danger-light p-2 rounded"
            >
              {error}
            </div>
          )}

          <div>
            <button
              type="submit"
              disabled={isLoading || !tokenInput.trim()}
              className={`btn btn-primary w-full ${
                isLoading ? "opacity-75 cursor-wait" : ""
              } ${!tokenInput.trim() ? "opacity-50 cursor-not-allowed" : ""}`}
            >
              {isLoading ? (
                <svg
                  className="animate-spin -ml-1 mr-3 h-5 w-5 text-white"
                  xmlns="http://www.w3.org/2000/svg"
                  fill="none"
                  viewBox="0 0 24 24"
                >
                  <circle
                    className="opacity-25"
                    cx="12"
                    cy="12"
                    r="10"
                    stroke="currentColor"
                    strokeWidth="4"
                  ></circle>
                  <path
                    className="opacity-75"
                    fill="currentColor"
                    d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
                  ></path>
                </svg>
              ) : null}
              {isLoading ? "Signing in..." : "Sign in"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
