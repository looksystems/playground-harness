type Constructor<T = {}> = new (...args: any[]) => T;

export interface Middleware {
  pre?(messages: Record<string, any>[], context: any): Promise<Record<string, any>[]> | Record<string, any>[];
  post?(message: Record<string, any>, context: any): Promise<Record<string, any>> | Record<string, any>;
}

export function HasMiddleware<TBase extends Constructor>(Base: TBase) {
  return class extends Base {
    middlewareStack: Middleware[] = [];

    use(middleware: Middleware): this {
      this.middlewareStack.push(middleware);
      return this;
    }

    removeMiddleware(middleware: Middleware): this {
      const idx = this.middlewareStack.indexOf(middleware);
      if (idx !== -1) {
        this.middlewareStack.splice(idx, 1);
      }
      return this;
    }

    async runPre(messages: Record<string, any>[], context: any): Promise<Record<string, any>[]> {
      for (const mw of this.middlewareStack) {
        if (mw.pre) {
          messages = await Promise.resolve(mw.pre(messages, context));
        }
      }
      return messages;
    }

    async runPost(message: Record<string, any>, context: any): Promise<Record<string, any>> {
      for (const mw of this.middlewareStack) {
        if (mw.post) {
          message = await Promise.resolve(mw.post(message, context));
        }
      }
      return message;
    }
  };
}
