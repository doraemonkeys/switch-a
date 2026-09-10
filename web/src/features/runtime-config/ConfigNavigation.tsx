import {
  ArrowRightLeft,
  KeyRound,
  Timer,
  Server,
  ChevronRight,
} from "lucide-react";
import { CONFIG_CATEGORIES } from "./schema";
import type { ConfigCategoryId, ConfigValues } from "./types";

const CATEGORY_ICONS = {
  routing: ArrowRightLeft,
  accounts: KeyRound,
  requests: Timer,
  system: Server,
};

export function ConfigNavigation({
  active,
  changes,
  onSelect,
}: {
  active?: ConfigCategoryId;
  changes: ConfigValues;
  onSelect: (id: ConfigCategoryId) => void;
}) {
  return (
    <nav className="config-navigation" aria-label="配置分类">
      {CONFIG_CATEGORIES.map((category) => {
        const Icon = CATEGORY_ICONS[category.id];
        const fields = category.groups.flatMap((group) => group.fields);
        const count = fields.filter((field) => field.key in changes).length;
        return (
          <button
            type="button"
            key={category.id}
            aria-current={active === category.id ? "page" : undefined}
            onClick={() => onSelect(category.id)}
          >
            <Icon size={18} aria-hidden="true" />
            <span>
              {category.title}
              <small>{fields.length} 项设置</small>
            </span>
            {count > 0 ? (
              <b aria-label={`${count} 项未保存`}>{count}</b>
            ) : (
              <ChevronRight size={15} aria-hidden="true" />
            )}
          </button>
        );
      })}
      <div className="config-navigation-note">
        <span className="config-pending-dot" /> 未保存的修改
        <p>切换分类会保留修改，完成后统一保存。</p>
      </div>
    </nav>
  );
}
