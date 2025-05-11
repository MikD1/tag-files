import { HttpClient } from "@angular/common/http";
import { inject, Injectable } from "@angular/core";

import type { Observable } from "rxjs";

@Injectable({
  providedIn: "root",
})
export class TagsApiService {
  private readonly baseUrl = "http://localhost:5001/api";
  private readonly http = inject(HttpClient);

  getTags(): Observable<string[]> {
    return this.http.get<string[]>(`${this.baseUrl}/tags`);
  }

  createTag(tag: string): Observable<void> {
    return this.http.post<void>(`${this.baseUrl}/tags`, tag);
  }
}
