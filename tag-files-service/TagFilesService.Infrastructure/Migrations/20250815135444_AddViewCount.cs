using System;
using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace TagFilesService.Infrastructure.Migrations
{
    /// <inheritdoc />
    public partial class AddViewCount : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.AddColumn<DateTime>(
                name: "LastView",
                table: "LibraryItems",
                type: "TEXT",
                nullable: true);

            migrationBuilder.AddColumn<int>(
                name: "ViewCount",
                table: "LibraryItems",
                type: "INTEGER",
                nullable: false,
                defaultValue: 0);
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropColumn(
                name: "LastView",
                table: "LibraryItems");

            migrationBuilder.DropColumn(
                name: "ViewCount",
                table: "LibraryItems");
        }
    }
}
