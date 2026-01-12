import React from "react";

export interface ConfigSectionProps {
    title: string;
    description: string;
    icon: string;
    children: React.ReactNode;
}

export function ConfigSection({
    title,
    description,
    icon,
    children,
}: ConfigSectionProps) {
    return (
        <fieldset className="space-y-4">
            <div className="flex items-start gap-3">
                <div className="w-10 h-10 rounded-xl bg-bg-secondary flex items-center justify-center shrink-0">
                    <span className="text-lg">{icon}</span>
                </div>
                <div>
                    <legend className="text-lg font-semibold text-text-primary">
                        {title}
                    </legend>
                    <p className="text-sm text-text-secondary">{description}</p>
                </div>
            </div>
            <div className="ml-13">{children}</div>
        </fieldset>
    );
}
