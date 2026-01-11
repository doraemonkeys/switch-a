import { createContext } from "react";
import { type ApiClient } from "./client";

export const ApiContext = createContext<ApiClient | null>(null);
