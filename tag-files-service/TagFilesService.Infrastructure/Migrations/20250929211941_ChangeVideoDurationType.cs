using System;
using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace TagFilesService.Infrastructure.Migrations
{
    /// <inheritdoc />
    public partial class ChangeVideoDurationType : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.AlterColumn<long>(
                name: "VideoDuration",
                table: "LibraryItems",
                type: "INTEGER",
                nullable: true,
                oldClrType: typeof(TimeSpan),
                oldType: "TEXT",
                oldNullable: true);
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.AlterColumn<TimeSpan>(
                name: "VideoDuration",
                table: "LibraryItems",
                type: "TEXT",
                nullable: true,
                oldClrType: typeof(long),
                oldType: "INTEGER",
                oldNullable: true);
        }
    }
}
