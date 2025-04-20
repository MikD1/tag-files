import { NgFor } from '@angular/common';
import { Component } from '@angular/core';
import { MatGridListModule } from '@angular/material/grid-list';
import { ImageCardComponent } from "../image-card/image-card.component";
import { FileMetadata } from '../../model/file-metadata';

@Component({
  selector: 'app-image-grid',
  imports: [NgFor, MatGridListModule, ImageCardComponent],
  templateUrl: './image-grid.component.html',
  styleUrl: './image-grid.component.scss'
})
export class ImageGridComponent {
  cols: number = 8;
  filesMetadata: FileMetadata[] = Array(21).fill({
    url: 'https://images.pexels.com/photos/2014422/pexels-photo-2014422.jpeg'
  });

  increaseCols() {
    if (this.cols < 12) {
      this.cols++;
    }
  }

  decreaseCols() {
    if (this.cols > 4) {
      this.cols--;
    }
  }
}
