import { ChangeDetectionStrategy, Component, inject } from '@angular/core';
import { AsyncPipe } from '@angular/common';
import { MatSidenavModule } from '@angular/material/sidenav';
import { TagsService } from '../../services/tags.service';
import { Observable } from 'rxjs';
import { MatExpansionModule } from '@angular/material/expansion';
import { MatListModule } from '@angular/material/list';
import { MatIconModule } from '@angular/material/icon';
import { RouterLink } from '@angular/router';
import { MatButtonModule } from '@angular/material/button';

@Component({
  selector: 'app-sidebar',
  imports: [MatSidenavModule, AsyncPipe, MatExpansionModule, MatListModule, MatIconModule, RouterLink, MatButtonModule],
  templateUrl: './sidebar.component.html',
  styleUrl: './sidebar.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class AppSidebarComponent {
  constructor() {
    this.tags = this.tagsService.getTags();
  }

  protected tags: Observable<string[]>;

  private readonly tagsService = inject(TagsService);
}
