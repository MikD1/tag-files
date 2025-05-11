import { ChangeDetectionStrategy, Component } from "@angular/core";

@Component({
  changeDetection: ChangeDetectionStrategy.OnPush,
  selector: "app-tags-page",
  imports: [],
  templateUrl: "./tags-page.component.html",
  styleUrl: "./tags-page.component.scss",
})
export class TagsPageComponent {}
