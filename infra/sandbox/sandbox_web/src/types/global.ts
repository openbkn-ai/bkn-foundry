/**
 * Global type definitions
 */

/** Route path */
export type RoutePath = '/' | '/templates' | '/sessions' | '/execute' | '/files';

/** Application menu item */
export interface MenuItem {
  key: string;
  label: string;
  path: RoutePath;
  icon: string;
}

/** Application state */
export interface AppState {
  currentRoute: RoutePath;
  sidebarCollapsed: boolean;
}
